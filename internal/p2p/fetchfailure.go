package p2p

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/uhhhm/reverb/internal/store/db"
)

// FetchFailureStore records which files are not replicating and why. It is an
// optional capability: a Puller whose store does not provide it still works,
// it simply retries everything every round the way it always did.
type FetchFailureStore interface {
	GetFileFetchFailure(ctx context.Context, arg db.GetFileFetchFailureParams) (db.FileFetchFailure, error)
	ListFileFetchFailures(ctx context.Context) ([]db.FileFetchFailure, error)
	UpsertFileFetchFailure(ctx context.Context, arg db.UpsertFileFetchFailureParams) error
	DeleteFileFetchFailure(ctx context.Context, arg db.DeleteFileFetchFailureParams) error
}

// The reasons the owner sees. They are stored rather than derived at read time
// so the explanation survives the round that produced it.
const (
	// ReasonUnstorablePath is the one a mixed-platform household meets: the
	// peer holds a file whose name this device's filesystem cannot write.
	ReasonUnstorablePath = "unstorable-path"
	// ReasonContentMismatch means the bytes the peer served did not hash to
	// what its manifest advertised. Retrying the same peer will not help until
	// its own scan catches up.
	ReasonContentMismatch = "content-mismatch"
	// ReasonUnavailable means the peer did not serve the file — it no longer
	// holds it, or the transfer failed.
	ReasonUnavailable = "unavailable"
)

// Backoff schedule. The first retry is soon enough that a peer that was simply
// asleep replicates on the next round or two; the cap is short enough that a
// file made fetchable by a migration or a re-download starts moving again
// within a day without the owner doing anything.
const (
	fetchRetryBase = 5 * time.Minute
	fetchRetryMax  = 24 * time.Hour
)

// candidate is one file this device is considering pulling, from one peer. The
// peer is half of the key: a round runs per peer, and most reasons a fetch
// fails are that peer's rather than the content's, so one device that no longer
// holds the file must not stop a healthy one from serving it.
type candidate struct {
	peerID      string
	contentHash string
	relPath     string
}

func (c candidate) key() db.GetFileFetchFailureParams {
	return db.GetFileFetchFailureParams{PeerID: c.peerID, ContentHash: c.contentHash}
}

// fetchFailures is the puller's view of the failure table. Every method
// degrades to a no-op when the store does not implement the capability, so the
// call sites stay free of nil checks.
type fetchFailures struct {
	q   FetchFailureStore
	now func() time.Time
}

func (f *fetchFailures) clock() time.Time {
	if f == nil || f.now == nil {
		return time.Now()
	}
	return f.now()
}

// ready reports whether a file is due for another attempt.
//
// A remote path that differs from the one we last failed on is treated as a
// fresh file rather than a continuation: the peer renamed it, which is exactly
// what a portable-name migration on that device does, and the whole point of
// that migration is that the file starts replicating again immediately.
func (f *fetchFailures) ready(ctx context.Context, c candidate) bool {
	if f == nil || f.q == nil {
		return true
	}
	row, err := f.q.GetFileFetchFailure(ctx, c.key())
	if err != nil {
		// No record, or the lookup failed. Either way, attempting is the
		// behaviour that replicates files.
		return true
	}
	if row.RelPath != c.relPath {
		return true
	}
	return !f.clock().Before(time.Unix(0, row.NextAttemptAt*int64(time.Millisecond)))
}

// noteUnattempted records a conclusion reached without attempting a fetch, so it does not
// advance the backoff — the selection filter reaches the same conclusion every
// round, and counting those as attempts would inflate the delay for a file
// nobody tried. The write is skipped when the stored row already says this, so
// a library full of unstorable names does not rewrite the table each round.
func (f *fetchFailures) noteUnattempted(ctx context.Context, c candidate, reason, detail string) {
	if f == nil || f.q == nil {
		return
	}
	now := f.clock().UnixMilli()
	arg := db.UpsertFileFetchFailureParams{
		PeerID:        c.peerID,
		ContentHash:   c.contentHash,
		RelPath:       c.relPath,
		Reason:        reason,
		Detail:        detail,
		FirstFailedAt: now,
		LastFailedAt:  now,
		// Nothing is waiting on a timer: this conclusion is reached afresh
		// every round, and stops being reached the moment the path changes.
		NextAttemptAt: now,
	}
	if row, err := f.q.GetFileFetchFailure(ctx, c.key()); err == nil {
		if row.RelPath == c.relPath && row.Reason == reason && row.Detail == detail {
			return
		}
		// An earlier attempt's count and start are the owner's history of this
		// file; re-describing why it is stuck should not erase them.
		arg.Attempts, arg.FirstFailedAt = row.Attempts, row.FirstFailedAt
	}
	f.upsert(ctx, arg)
}

// recordAttempt registers a real failed attempt and pushes the next one further out.
func (f *fetchFailures) recordAttempt(ctx context.Context, c candidate, cause error) {
	if f == nil || f.q == nil {
		return
	}
	now := f.clock()
	reason, detail := classifyFetchFailure(cause)
	attempts := int64(1)
	firstFailed := now.UnixMilli()
	if row, err := f.q.GetFileFetchFailure(ctx, c.key()); err == nil && row.RelPath == c.relPath {
		attempts = row.Attempts + 1
		firstFailed = row.FirstFailedAt
	}
	f.upsert(ctx, db.UpsertFileFetchFailureParams{
		PeerID:        c.peerID,
		ContentHash:   c.contentHash,
		RelPath:       c.relPath,
		Reason:        reason,
		Detail:        detail,
		Attempts:      attempts,
		FirstFailedAt: firstFailed,
		LastFailedAt:  now.UnixMilli(),
		NextAttemptAt: now.Add(retryDelay(attempts)).UnixMilli(),
	})
}

// clear forgets a file that has started succeeding, so a later failure starts
// its backoff from the beginning rather than from where the old one left off.
func (f *fetchFailures) clear(ctx context.Context, c candidate) {
	if f == nil || f.q == nil {
		return
	}
	_ = f.q.DeleteFileFetchFailure(ctx, db.DeleteFileFetchFailureParams{PeerID: c.peerID, ContentHash: c.contentHash})
}

// prune forgets what this peer's rows claim once it is no longer true.
//
// A row is the answer to "why has this track not arrived", so it has to stop
// existing when the track is no longer missing: the content may have come from
// another device, the owner may have deleted it, or this peer may have stopped
// offering it. Without this the panel fills up with tracks that are fine, which
// is the same confusion it exists to remove — an owner cannot act on a list
// they have learned to distrust.
//
// wanted is the set of content hashes this round still expects from the peer,
// including the ones held back by their own backoff.
func (f *fetchFailures) prune(ctx context.Context, peerID string, wanted map[string]bool) {
	if f == nil || f.q == nil {
		return
	}
	rows, err := f.q.ListFileFetchFailures(ctx)
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.PeerID != peerID || wanted[row.ContentHash] {
			continue
		}
		_ = f.q.DeleteFileFetchFailure(ctx, db.DeleteFileFetchFailureParams{
			PeerID:      row.PeerID,
			ContentHash: row.ContentHash,
		})
	}
}

func (f *fetchFailures) upsert(ctx context.Context, arg db.UpsertFileFetchFailureParams) {
	if err := f.q.UpsertFileFetchFailure(ctx, arg); err != nil {
		// Losing the record costs a retry, not a file.
		return
	}
}

// retryDelay doubles with each attempt up to the cap. Shifting past the cap
// would overflow, so the exponent is bounded before it is applied.
func retryDelay(attempts int64) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	const maxShift = 12 // 5m << 12 is already well past the 24h cap
	if attempts-1 > maxShift {
		return fetchRetryMax
	}
	d := fetchRetryBase << (attempts - 1)
	if d > fetchRetryMax {
		return fetchRetryMax
	}
	return d
}

// classifyFetchFailure turns a fetch error into the reason the owner sees plus
// the detail behind it. The reason is a stable token the UI can phrase; the
// detail is the error text, which is what makes an unexpected failure
// diagnosable without reading the log.
func classifyFetchFailure(err error) (reason, detail string) {
	if err == nil {
		return ReasonUnavailable, ""
	}
	detail = err.Error()
	if errors.Is(err, ErrContentMismatch) {
		return ReasonContentMismatch, detail
	}
	// Everything else — a refused stream, a timeout, a peer that no longer
	// holds the content — is the peer not serving the file. They differ in
	// cause but not in what the owner can do about them, and the detail
	// carries the distinction for anyone who needs it.
	return ReasonUnavailable, detail
}

// unstorableDetail explains, in the owner's terms, why a name this peer minted
// cannot be written here.
func unstorableDetail(relPath string) string {
	return fmt.Sprintf("this device's filesystem cannot store the name %q", relPath)
}
