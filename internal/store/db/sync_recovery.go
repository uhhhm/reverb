package db

import "context"

// QuarantineSyncChange runs inside the caller's transaction together with the
// vector reset, so a crash cannot acknowledge a row that has been removed.
func (q *Queries) QuarantineSyncChange(ctx context.Context, revision int64, payload, author string) error {
	if err := q.quarantine(ctx, revision, payload, "invalid remote signature"); err != nil {
		return err
	}
	return q.ResetSyncVector(ctx, author)
}

// QuarantineMalformedSyncChange removes a row whose value is not valid JSON.
// Unlike a corrupt signature there is nothing to ask for: the author's own copy
// is what it sent, and the boundary refuses it now, so resetting the author's
// vector would only pull the same row back. Clearing its pending projection is
// what stops the retry loop from reporting a failure that can never succeed.
func (q *Queries) QuarantineMalformedSyncChange(ctx context.Context, revision int64, payload string) error {
	if err := q.quarantine(ctx, revision, payload, "value is not valid JSON"); err != nil {
		return err
	}
	return q.CompleteSyncProjection(ctx, revision)
}

// quarantine keeps a copy of a row for diagnosis and drops it from the log.
func (q *Queries) quarantine(ctx context.Context, revision int64, payload, reason string) error {
	if err := q.QuarantineSyncCopy(ctx, QuarantineSyncCopyParams{Revision: revision, ChangeJson: payload, Reason: reason}); err != nil {
		return err
	}
	return q.DeleteCorruptSyncChange(ctx, revision)
}
