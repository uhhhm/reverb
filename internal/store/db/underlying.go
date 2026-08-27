package db

// Hand-written extensions to the sqlc-generated Queries type live in
// files like this one. sqlc overwrites only the files it generates
// (db.go, models.go, querier.go, *.sql.go), so anything placed inside
// those is lost on the next `make gen`.

// UnderlyingDB exposes the raw DBTX so callers can open transactions or
// reach driver-specific facilities.
func (q *Queries) UnderlyingDB() DBTX { return q.db }
