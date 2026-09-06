package db

import "context"

// QuarantineSyncChange runs inside the caller's transaction together with the
// vector reset, so a crash cannot acknowledge a row that has been removed.
func (q *Queries) QuarantineSyncChange(ctx context.Context, revision int64, payload, author string) error {
	if err := q.QuarantineSyncCopy(ctx, QuarantineSyncCopyParams{Revision: revision, ChangeJson: payload}); err != nil {
		return err
	}
	if err := q.DeleteCorruptSyncChange(ctx, revision); err != nil {
		return err
	}
	return q.ResetSyncVector(ctx, author)
}
