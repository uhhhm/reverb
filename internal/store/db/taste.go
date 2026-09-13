package db

import "context"

// TastePlayRow is one play with the catalog names the taste profile reads.
// Seq is the play's rowid: the order this device stored it in.
type TastePlayRow struct {
	Seq       int64
	Title     string
	Artist    string
	PlayedAt  int64
	Completed bool
}

// ListTastePlaysAfter returns up to limit plays stored after seq, in the
// order they were stored. It reads rowid, which sqlc cannot express, so it
// is written by hand.
func (q *Queries) ListTastePlaysAfter(ctx context.Context, after int64, limit int) ([]TastePlayRow, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT p.rowid, e.title, e.artist, p.played_at, p.completed
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.rowid > ? ORDER BY p.rowid LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TastePlayRow
	for rows.Next() {
		var r TastePlayRow
		var completed int64
		if err := rows.Scan(&r.Seq, &r.Title, &r.Artist, &r.PlayedAt, &completed); err != nil {
			return nil, err
		}
		r.Completed = completed != 0
		out = append(out, r)
	}
	return out, rows.Err()
}
