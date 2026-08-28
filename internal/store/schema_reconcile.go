package store

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
)

// Schema reconciliation repairs databases whose goose version table claims
// migrations that the schema does not reflect — a table or column stamped as
// applied but absent. Observed on desktop DBs where a whole batch of version
// rows landed without their DDL, leaving 0027's track_override missing and
// aborting 0029's ALTER with "no such table".
//
// The expected schema is replayed from the migrations themselves rather than
// restated here, so a new migration needs no matching repair code. Only
// structure is reconciled; rows written by data migrations are not restored.

type effectKind int

const (
	effectTable effectKind = iota
	effectIndex
	effectColumn
)

// schemaEffect is one structural object a migration creates, paired with the
// statement that creates it.
type schemaEffect struct {
	kind  effectKind
	table string
	name  string // table, index, or column name depending on kind
	sql   string
}

// goMigrationDDL supplies the structural statements of Go migrations, which are
// not readable from the embedded SQL. Only drops are needed: every table
// upSingleUser creates is also created by SQL migrations 0024 and 0029, whereas
// the legacy tables it removes are created in SQL and would otherwise look like
// objects that ought to exist. These statements are classified, never executed.
//
// MUST stay in sync with upSingleUser in migrate_single_user.go — any table
// dropped there must be listed here or the reconciler will resurrect it.
// latestMigrationVersion in store.go derives its Go-migration max from this map.
var goMigrationDDL = map[int64][]string{
	25: {
		"DROP TABLE sessions",
		"DROP TABLE invites",
		"DROP TABLE requests",
		"DROP TABLE notifications",
		"DROP TABLE roles",
	},
}

var (
	reCreateTable = regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?"?(\w+)"?`)
	reCreateIndex = regexp.MustCompile(`(?is)^CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?"?(\w+)"?\s+ON\s+"?(\w+)"?`)
	reAddColumn   = regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+"?(\w+)"?\s+ADD\s+(?:COLUMN\s+)?"?(\w+)"?`)
	reDropTable   = regexp.MustCompile(`(?is)^DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?"?(\w+)"?`)
	reDropIndex   = regexp.MustCompile(`(?is)^DROP\s+INDEX\s+(?:IF\s+EXISTS\s+)?"?(\w+)"?`)
	reDropColumn  = regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+"?(\w+)"?\s+DROP\s+(?:COLUMN\s+)?"?(\w+)"?`)
)

// reconcileSchema recreates schema objects that the applied migrations say
// should exist but the database lacks, and reports how many statements it ran.
// A database matching its stamped version is left untouched. Repairs run in one
// transaction, so a failure leaves the schema exactly as it was.
func (s *Store) reconcileSchema() (int, error) {
	if s.sql == nil {
		return 0, nil
	}
	var stamped int
	if err := s.sql.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, goose.TableName(),
	).Scan(&stamped); err != nil {
		return 0, fmt.Errorf("inspect goose version table: %w", err)
	}
	if stamped == 0 {
		return 0, nil // fresh DB — goose.Up builds the whole schema
	}
	cur, err := goose.GetDBVersion(s.sql)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}

	effects, err := expectedSchema(cur)
	if err != nil {
		return 0, err
	}

	tx, err := s.sql.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	repaired := 0
	for _, e := range effects {
		missing, err := effectMissing(tx, e)
		if err != nil {
			return 0, err
		}
		if !missing {
			continue
		}
		if _, err := tx.Exec(e.sql); err != nil {
			return 0, fmt.Errorf("restore %s: %w", e.name, err)
		}
		repaired++
	}
	if repaired == 0 {
		return 0, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return repaired, nil
}

// effectMissing reports whether the object an effect creates is absent. A
// column whose table does not exist is not missing but unreachable: the table
// is either restored earlier in the same pass or built by DDL this replay
// cannot see, and ALTER would fail either way.
func effectMissing(tx *sql.Tx, e schemaEffect) (bool, error) {
	var n int
	var err error
	switch e.kind {
	case effectTable:
		err = tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, e.name).Scan(&n)
	case effectIndex:
		err = tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, e.name).Scan(&n)
	case effectColumn:
		var tbl int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, e.table).Scan(&tbl); err != nil {
			return false, err
		}
		if tbl == 0 {
			return false, nil
		}
		err = tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, e.table, e.name).Scan(&n)
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", e.name, err)
	}
	return n == 0, nil
}

// expectedSchema replays the Up statements of every migration through version
// cur, in order, and returns the objects that should exist at the end. Drops
// remove earlier effects, so a table a later migration deletes is not expected
// back.
func expectedSchema(cur int64) ([]schemaEffect, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	versions := make([]int64, 0, len(entries)+len(goMigrationDDL))
	sqlByVersion := map[int64][]string{}
	for _, e := range entries {
		name := e.Name()
		v, ok := migrationVersion(name)
		if !ok || v > cur {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		sqlByVersion[v] = splitStatements(upSection(string(body)))
		versions = append(versions, v)
	}
	for v, stmts := range goMigrationDDL {
		if v > cur {
			continue
		}
		sqlByVersion[v] = append(sqlByVersion[v], stmts...)
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })

	var effects []schemaEffect
	seen := map[int64]bool{}
	for _, v := range versions {
		if seen[v] {
			continue
		}
		seen[v] = true
		for _, stmt := range sqlByVersion[v] {
			effects = applyStatement(effects, stmt)
		}
	}
	return effects, nil
}

// applyStatement folds one migration statement into the expected object set.
// Statements that are neither DDL nor recognised (inserts, updates, deletes)
// leave the set unchanged.
func applyStatement(effects []schemaEffect, stmt string) []schemaEffect {
	switch {
	case reCreateTable.MatchString(stmt):
		m := reCreateTable.FindStringSubmatch(stmt)
		return append(effects, schemaEffect{kind: effectTable, table: m[1], name: m[1], sql: stmt})
	case reCreateIndex.MatchString(stmt):
		m := reCreateIndex.FindStringSubmatch(stmt)
		return append(effects, schemaEffect{kind: effectIndex, table: m[2], name: m[1], sql: stmt})
	case reDropColumn.MatchString(stmt):
		m := reDropColumn.FindStringSubmatch(stmt)
		return filterEffects(effects, func(e schemaEffect) bool {
			return !(e.kind == effectColumn && e.table == m[1] && e.name == m[2])
		})
	case reAddColumn.MatchString(stmt):
		m := reAddColumn.FindStringSubmatch(stmt)
		return append(effects, schemaEffect{kind: effectColumn, table: m[1], name: m[2], sql: stmt})
	case reDropTable.MatchString(stmt):
		m := reDropTable.FindStringSubmatch(stmt)
		return filterEffects(effects, func(e schemaEffect) bool { return e.table != m[1] })
	case reDropIndex.MatchString(stmt):
		m := reDropIndex.FindStringSubmatch(stmt)
		return filterEffects(effects, func(e schemaEffect) bool {
			return !(e.kind == effectIndex && e.name == m[1])
		})
	}
	return effects
}

func filterEffects(effects []schemaEffect, keep func(schemaEffect) bool) []schemaEffect {
	out := effects[:0]
	for _, e := range effects {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

// migrationVersion extracts the numeric prefix of a migration filename
// (0022_download_job_canonical.sql -> 22).
func migrationVersion(name string) (int64, bool) {
	if !strings.HasSuffix(name, ".sql") {
		return 0, false
	}
	idx := strings.IndexByte(name, '_')
	if idx <= 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(name[:idx], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// upSection returns the body between the goose Up and Down annotations.
func upSection(body string) string {
	lines := strings.Split(body, "\n")
	var out []string
	in := false
	for _, line := range lines {
		switch annotation(line) {
		case "up":
			in = true
			continue
		case "down":
			in = false
			continue
		}
		if in {
			out = append(out, stripComment(line))
		}
	}
	return strings.Join(out, "\n")
}

// annotation reports which goose section marker a line is, if any.
func annotation(line string) string {
	f := strings.Fields(line)
	if len(f) < 3 || f[0] != "--" || f[1] != "+goose" {
		return ""
	}
	return strings.ToLower(f[2])
}

// stripComment removes a trailing line comment, ignoring `--` inside a string
// literal. Handles escaped single quotes (”) inside strings.
func stripComment(line string) string {
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\'':
			if inQuote && i+1 < len(line) && line[i+1] == '\'' {
				i++ // escaped quote '' — stay inside string
			} else {
				inQuote = !inQuote
			}
		case !inQuote && line[i] == '-' && i+1 < len(line) && line[i+1] == '-':
			return line[:i]
		}
	}
	return line
}

// splitStatements breaks SQL on semicolons outside string literals and returns
// the non-empty statements, trimmed. Handles escaped single quotes (”).
func splitStatements(body string) []string {
	var stmts []string
	inQuote := false
	start := 0
	for i := 0; i < len(body); i++ {
		switch {
		case body[i] == '\'':
			if inQuote && i+1 < len(body) && body[i+1] == '\'' {
				i++ // escaped quote '' — stay inside string
			} else {
				inQuote = !inQuote
			}
		case !inQuote && body[i] == ';':
			if stmt := strings.TrimSpace(body[start:i]); stmt != "" {
				stmts = append(stmts, stmt)
			}
			start = i + 1
		}
	}
	if stmt := strings.TrimSpace(body[start:]); stmt != "" {
		stmts = append(stmts, stmt)
	}
	return stmts
}
