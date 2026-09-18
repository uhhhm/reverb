package db

import (
	"context"
	"database/sql"
	"errors"
)

// TastePlayRow is one play with the catalog names the taste profile reads.
// Seq is the play's rowid: the order this device stored it in. A rowid is
// reused once its row is deleted, so ID is what identifies the play.
type TastePlayRow struct {
	ID        string
	Seq       int64
	Title     string
	Artist    string
	PlayedAt  int64
	Completed bool
}

// tastePlaysFrom selects the plays the taste profile reads. Both reads below
// share it so a play can never be eligible for one and not the other.
const tastePlaysFrom = `FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.qualified = 1`

// ListTastePlaysAfter returns up to limit plays stored after seq, in the
// order they were stored. It reads rowid, which sqlc cannot express, so it
// is written by hand.
func (q *Queries) ListTastePlaysAfter(ctx context.Context, after int64, limit int) ([]TastePlayRow, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT p.id, p.rowid, e.title, e.artist, p.played_at, p.completed
`+tastePlaysFrom+` AND p.rowid > ? ORDER BY p.rowid LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TastePlayRow
	for rows.Next() {
		var r TastePlayRow
		var completed int64
		if err := rows.Scan(&r.ID, &r.Seq, &r.Title, &r.Artist, &r.PlayedAt, &completed); err != nil {
			return nil, err
		}
		r.Completed = completed != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// TastePlayIDAt returns the id of the qualified play stored at rowid seq, or
// "" when none is. Deleting a play frees its rowid for the next insert to
// take, so the taste profile checks the id at its cursor to notice that the
// play it folded in was replaced rather than followed.
func (q *Queries) TastePlayIDAt(ctx context.Context, seq int64) (string, error) {
	var id string
	err := q.db.QueryRowContext(ctx, `SELECT p.id
`+tastePlaysFrom+` AND p.rowid = ?`, seq).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}
