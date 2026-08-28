package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/store/db"
)

// SyncStore provides changelog helpers around Querier.
// Querier is the pairing Querier (minimal seam); sync methods are accessed via
// type assertion on the underlying *db.Queries so we avoid extending Querier
// and keep device.go untouched. This handles the historical GetMaxSyncRevision
// return type (interface{} in generated code) transparently.
type SyncStore struct {
	q      Querier
	policy MergePolicy
	mu     sync.Mutex
	hlc    *HLC
}

// NewSyncStore creates a store with default LWWPolicy.
func NewSyncStore(q Querier) *SyncStore {
	return &SyncStore{q: q, policy: LWWPolicy{}, hlc: NewHLC()}
}

// NewSyncStoreWithPolicy creates a store with a custom merge policy (for tests).
func NewSyncStoreWithPolicy(q Querier, p MergePolicy) *SyncStore {
	if p == nil {
		p = LWWPolicy{}
	}
	return &SyncStore{q: q, policy: p, hlc: NewHLC()}
}

func marshalValue(ch SyncChange) (string, error) {
	if ch.Field == "__deleted" {
		return "true", nil
	}
	if ch.Value == nil {
		return "null", nil
	}
	b, err := json.Marshal(ch.Value)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalValue(field, valueJSON string) any {
	if field == "__deleted" {
		return nil
	}
	if valueJSON == "" || valueJSON == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(valueJSON), &v); err != nil {
		return valueJSON
	}
	return v
}

func dbToSyncChange(row db.SyncChange) SyncChange {
	return SyncChange{
		EntityType: row.EntityType,
		EntityID:   row.EntityID,
		Field:      row.Field,
		Value:      unmarshalValue(row.Field, row.ValueJson),
		UpdatedAt:  row.UpdatedAt,
		DeviceID:   row.DeviceID,
		Revision:   row.Revision,
		HLC:        row.Hlc,
		Seq:        row.Seq,
	}
}

// --- helpers that reach sync methods via type assertion ---

func (s *SyncStore) appendSyncChange(ctx context.Context, arg db.AppendSyncChangeParams) (int64, error) {
	if qq, ok := any(s.q).(interface {
		AppendSyncChange(context.Context, db.AppendSyncChangeParams) (int64, error)
	}); ok {
		return qq.AppendSyncChange(ctx, arg)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.AppendSyncChange(ctx, arg)
	}
	return 0, fmt.Errorf("querier does not support AppendSyncChange")
}

func (s *SyncStore) appendSyncChangeWithHLC(ctx context.Context, arg db.AppendSyncChangeWithHLCParams) (int64, error) {
	if qq, ok := any(s.q).(interface {
		AppendSyncChangeWithHLC(context.Context, db.AppendSyncChangeWithHLCParams) (int64, error)
	}); ok {
		return qq.AppendSyncChangeWithHLC(ctx, arg)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.AppendSyncChangeWithHLC(ctx, arg)
	}
	// Fallback to legacy without HLC (for mocks that only implement old).
	return s.appendSyncChange(ctx, db.AppendSyncChangeParams{
		DeviceID:   arg.DeviceID,
		EntityType: arg.EntityType,
		EntityID:   arg.EntityID,
		Field:      arg.Field,
		ValueJson:  arg.ValueJson,
		UpdatedAt:  arg.UpdatedAt,
	})
}

func (s *SyncStore) listSyncChangesSince(ctx context.Context, arg db.ListSyncChangesSinceParams) ([]db.SyncChange, error) {
	if qq, ok := any(s.q).(interface {
		ListSyncChangesSince(context.Context, db.ListSyncChangesSinceParams) ([]db.SyncChange, error)
	}); ok {
		return qq.ListSyncChangesSince(ctx, arg)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.ListSyncChangesSince(ctx, arg)
	}
	return nil, fmt.Errorf("querier does not support ListSyncChangesSince")
}

func (s *SyncStore) getMaxSyncRevision(ctx context.Context) (int64, error) {
	if qq, ok := any(s.q).(interface {
		GetMaxSyncRevision(context.Context) (int64, error)
	}); ok {
		return qq.GetMaxSyncRevision(ctx)
	}
	if qq, ok := any(s.q).(interface {
		GetMaxSyncRevision(context.Context) (interface{}, error)
	}); ok {
		v, err := qq.GetMaxSyncRevision(ctx)
		if err != nil {
			return 0, err
		}
		switch vv := v.(type) {
		case int64:
			return vv, nil
		case int:
			return int64(vv), nil
		case int32:
			return int64(vv), nil
		case int16:
			return int64(vv), nil
		case float64:
			return int64(vv), nil
		case nil:
			return 0, nil
		case string:
			return 0, nil
		default:
			return 0, fmt.Errorf("unexpected GetMaxSyncRevision type %T", v)
		}
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		v, err := dbq.GetMaxSyncRevision(ctx)
		if err != nil {
			return 0, err
		}
		switch vv := v.(type) {
		case int64:
			return vv, nil
		case int:
			return int64(vv), nil
		case nil:
			return 0, nil
		default:
			return 0, fmt.Errorf("unexpected GetMaxSyncRevision type %T", v)
		}
	}
	return 0, fmt.Errorf("querier does not support GetMaxSyncRevision")
}

func (s *SyncStore) getMaxHLC(ctx context.Context) (int64, error) {
	if qq, ok := any(s.q).(interface {
		GetMaxHLC(context.Context) (int64, error)
	}); ok {
		return qq.GetMaxHLC(ctx)
	}
	if qq, ok := any(s.q).(interface {
		GetMaxHLC(context.Context) (interface{}, error)
	}); ok {
		v, err := qq.GetMaxHLC(ctx)
		if err != nil {
			return 0, err
		}
		switch vv := v.(type) {
		case int64:
			return vv, nil
		case int:
			return int64(vv), nil
		case int32:
			return int64(vv), nil
		case float64:
			return int64(vv), nil
		case nil:
			return 0, nil
		case string:
			return 0, nil
		default:
			return 0, fmt.Errorf("unexpected GetMaxHLC type %T", v)
		}
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		v, err := dbq.GetMaxHLC(ctx)
		if err != nil {
			return 0, err
		}
		switch vv := v.(type) {
		case int64:
			return vv, nil
		case int:
			return int64(vv), nil
		case nil:
			return 0, nil
		default:
			return 0, fmt.Errorf("unexpected GetMaxHLC type %T", v)
		}
	}
	// No HLC column on old mocks — treat as 0.
	return 0, nil
}

func (s *SyncStore) getLatestSyncChangeForField(ctx context.Context, arg db.GetLatestSyncChangeForFieldParams) (db.SyncChange, error) {
	if qq, ok := any(s.q).(interface {
		GetLatestSyncChangeForField(context.Context, db.GetLatestSyncChangeForFieldParams) (db.SyncChange, error)
	}); ok {
		return qq.GetLatestSyncChangeForField(ctx, arg)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.GetLatestSyncChangeForField(ctx, arg)
	}
	return db.SyncChange{}, fmt.Errorf("querier does not support GetLatestSyncChangeForField")
}

func (s *SyncStore) getSyncCursor(ctx context.Context, deviceID string) (db.SyncCursor, error) {
	if qq, ok := any(s.q).(interface {
		GetSyncCursor(context.Context, string) (db.SyncCursor, error)
	}); ok {
		return qq.GetSyncCursor(ctx, deviceID)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.GetSyncCursor(ctx, deviceID)
	}
	return db.SyncCursor{}, fmt.Errorf("querier does not support GetSyncCursor")
}

func (s *SyncStore) upsertSyncCursor(ctx context.Context, arg db.UpsertSyncCursorParams) error {
	if qq, ok := any(s.q).(interface {
		UpsertSyncCursor(context.Context, db.UpsertSyncCursorParams) error
	}); ok {
		return qq.UpsertSyncCursor(ctx, arg)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.UpsertSyncCursor(ctx, arg)
	}
	return fmt.Errorf("querier does not support UpsertSyncCursor")
}

func (s *SyncStore) getSyncVector(ctx context.Context, deviceID string) (db.SyncVector, error) {
	if qq, ok := any(s.q).(interface {
		GetSyncVector(context.Context, string) (db.SyncVector, error)
	}); ok {
		return qq.GetSyncVector(ctx, deviceID)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.GetSyncVector(ctx, deviceID)
	}
	return db.SyncVector{}, fmt.Errorf("querier does not support GetSyncVector")
}

func (s *SyncStore) upsertSyncVector(ctx context.Context, arg db.UpsertSyncVectorParams) error {
	if qq, ok := any(s.q).(interface {
		UpsertSyncVector(context.Context, db.UpsertSyncVectorParams) error
	}); ok {
		return qq.UpsertSyncVector(ctx, arg)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.UpsertSyncVector(ctx, arg)
	}
	return fmt.Errorf("querier does not support UpsertSyncVector")
}

func (s *SyncStore) ensureHLC(ctx context.Context) {
	if s.hlc == nil {
		s.hlc = NewHLC()
	}
	if s.hlc.Current() != 0 {
		return
	}
	if h, err := s.getMaxHLC(ctx); err == nil && h > 0 {
		s.hlc.Observe(h)
	}
}

// nextSeqLocked returns the next seq for deviceID without writing.
// Caller is responsible for upserting the vector with the final HLC in the same critical section.
// Caller must hold s.mu or be inside a transaction's txStore.mu.
func (s *SyncStore) nextSeqLocked(ctx context.Context, deviceID string) (int64, int64, error) {
	s.ensureHLC(ctx)
	vec, err := s.getSyncVector(ctx, deviceID)
	var curSeq, curHLC int64
	if err == nil {
		curSeq = vec.Seq
		curHLC = vec.Hlc
		if curHLC > s.hlc.Current() {
			s.hlc.Observe(curHLC)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, err
	}
	newSeq := curSeq + 1
	newHLC := s.hlc.Current()
	return newSeq, newHLC, nil
}

// AppendChange appends a single change for deviceID. Value is marshaled to value_json.
func (s *SyncStore) AppendChange(ctx context.Context, deviceID string, ch SyncChange) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	valueJSON, err := marshalValue(ch)
	if err != nil {
		return 0, err
	}
	rowHLC, rowSeq, err := s.prepareHLCVector(ctx, deviceID, ch)
	if err != nil {
		return 0, err
	}
	return s.appendSyncChangeWithHLC(ctx, db.AppendSyncChangeWithHLCParams{
		DeviceID:   deviceID,
		EntityType: ch.EntityType,
		EntityID:   ch.EntityID,
		Field:      ch.Field,
		ValueJson:  valueJSON,
		UpdatedAt:  ch.UpdatedAt,
		Hlc:        rowHLC,
		Seq:        rowSeq,
	})
}

func (s *SyncStore) appendChangeLocked(ctx context.Context, deviceID string, ch SyncChange) (int64, error) {
	valueJSON, err := marshalValue(ch)
	if err != nil {
		return 0, err
	}
	rowHLC, rowSeq, err := s.prepareHLCVector(ctx, deviceID, ch)
	if err != nil {
		return 0, err
	}
	return s.appendSyncChangeWithHLC(ctx, db.AppendSyncChangeWithHLCParams{
		DeviceID:   deviceID,
		EntityType: ch.EntityType,
		EntityID:   ch.EntityID,
		Field:      ch.Field,
		ValueJson:  valueJSON,
		UpdatedAt:  ch.UpdatedAt,
		Hlc:        rowHLC,
		Seq:        rowSeq,
	})
}

// ListSince returns changes with revision > since, ordered by revision ASC.
func (s *SyncStore) ListSince(ctx context.Context, since int64, limit int64) ([]SyncChange, error) {
	if limit <= 0 {
		limit = 10000
	}
	rows, err := s.listSyncChangesSince(ctx, db.ListSyncChangesSinceParams{
		Revision: since,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]SyncChange, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbToSyncChange(r))
	}
	if out == nil {
		out = []SyncChange{}
	}
	return out, nil
}

// ListSinceHLC returns changes with hlc > since, ordered by hlc ASC (P2P).
func (s *SyncStore) ListSinceHLC(ctx context.Context, since int64, limit int64) ([]SyncChange, error) {
	if limit <= 0 {
		limit = 10000
	}
	q, ok := any(s.q).(interface {
		ListSyncChangesSinceHLC(context.Context, db.ListSyncChangesSinceHLCParams) ([]db.SyncChange, error)
	})
	if !ok {
		if dbq, ok := any(s.q).(*db.Queries); ok {
			q = dbq
		} else {
			return nil, fmt.Errorf("querier does not support ListSyncChangesSinceHLC")
		}
	}
	rows, err := q.ListSyncChangesSinceHLC(ctx, db.ListSyncChangesSinceHLCParams{Hlc: since, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]SyncChange, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbToSyncChange(r))
	}
	if out == nil {
		out = []SyncChange{}
	}
	return out, nil
}

// ListSinceVector returns changes not yet seen by peer vector (seq > peerSeq per device).
// It pages through the global log so that filtering does not truncate when the
// log exceeds the outbound cap.
func (s *SyncStore) ListSinceVector(ctx context.Context, vector map[string]int64, limit int64) ([]SyncChange, error) {
	if limit <= 0 {
		limit = 10000
	}
	if len(vector) == 0 {
		return s.ListSince(ctx, 0, limit)
	}
	const batch = 1000
	var out []SyncChange
	var cursor int64
	for int64(len(out)) < limit {
		need := limit - int64(len(out))
		fetch := batch
		if need < int64(batch) {
			fetch = int(need)
			if fetch < 1 {
				fetch = 1
			}
		}
		if fetch > 10000 {
			fetch = 10000
		}
		rows, err := s.ListSince(ctx, cursor, int64(fetch))
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, ch := range rows {
			if seen, ok := vector[ch.DeviceID]; ok {
				if ch.Seq != 0 {
					if ch.Seq <= seen {
						continue
					}
				} else {
					// Legacy rows (pre-0029) have seq==0. After backfill (0030) none remain,
					// but for old DBs not yet migrated, treat legacy as seen if peer already
					// has any seq for this device to avoid infinite resend every round.
					if seen > 0 {
						continue
					}
				}
			}
			out = append(out, ch)
			if int64(len(out)) >= limit {
				break
			}
		}
		if len(rows) < fetch {
			break
		}
		cursor = rows[len(rows)-1].Revision
		// Safety: avoid infinite loop if cursor doesn't advance (shouldn't happen).
		if cursor == 0 {
			break
		}
	}
	if out == nil {
		out = []SyncChange{}
	}
	if int64(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetMaxRevision returns the current global revision (0 if none).
func (s *SyncStore) GetMaxRevision(ctx context.Context) (int64, error) {
	return s.getMaxSyncRevision(ctx)
}

// GetMaxHLC returns the max HLC (0 if none).
func (s *SyncStore) GetMaxHLC(ctx context.Context) (int64, error) {
	return s.getMaxHLC(ctx)
}

// GetLatestForField returns the latest change for entityType+entityId+field, or nil if none.
func (s *SyncStore) GetLatestForField(ctx context.Context, entityType, entityID, field string) (*SyncChange, error) {
	row, err := s.getLatestSyncChangeForField(ctx, db.GetLatestSyncChangeForFieldParams{
		EntityType: entityType,
		EntityID:   entityID,
		Field:      field,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	sc := dbToSyncChange(row)
	return &sc, nil
}

// GetCursor returns the stored cursor revision for deviceID (0 if none).
func (s *SyncStore) GetCursor(ctx context.Context, deviceID string) (int64, error) {
	cur, err := s.getSyncCursor(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return cur.Revision, nil
}

// SetCursor upserts the cursor for deviceID.
func (s *SyncStore) SetCursor(ctx context.Context, deviceID string, rev int64) error {
	return s.upsertSyncCursor(ctx, db.UpsertSyncCursorParams{
		DeviceID: deviceID,
		Revision: rev,
	})
}

// GetVector returns the stored sync vector for deviceID (0,0 if none).
func (s *SyncStore) GetVector(ctx context.Context, deviceID string) (seq, hlc int64, err error) {
	vec, err := s.getSyncVector(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	return vec.Seq, vec.Hlc, nil
}

// SetVector upserts the vector for deviceID.
func (s *SyncStore) SetVector(ctx context.Context, deviceID string, seq, hlc int64) error {
	return s.upsertSyncVector(ctx, db.UpsertSyncVectorParams{DeviceID: deviceID, Seq: seq, Hlc: hlc})
}

// GetVectorMap returns all sync vectors as map[deviceID]{seq,hlc}.
func (s *SyncStore) GetVectorMap(ctx context.Context) (map[string]int64, map[string]int64, error) {
	q, ok := any(s.q).(interface {
		ListSyncVectors(context.Context) ([]db.SyncVector, error)
	})
	if !ok {
		if dbq, ok := any(s.q).(*db.Queries); ok {
			q = dbq
		} else {
			return nil, nil, fmt.Errorf("querier does not support ListSyncVectors")
		}
	}
	rows, err := q.ListSyncVectors(ctx)
	if err != nil {
		return nil, nil, err
	}
	seqMap := make(map[string]int64, len(rows))
	hlcMap := make(map[string]int64, len(rows))
	for _, r := range rows {
		seqMap[r.DeviceID] = r.Seq
		hlcMap[r.DeviceID] = r.Hlc
	}
	return seqMap, hlcMap, nil
}

// ValidateDevice checks that deviceID exists. Returns sql.ErrNoRows wrapped if not found.
func (s *SyncStore) ValidateDevice(ctx context.Context, deviceID string) error {
	if deviceID == "" {
		return fmt.Errorf("missing deviceId")
	}
	if _, err := s.q.GetDeviceByID(ctx, deviceID); err != nil {
		return err
	}
	return nil
}

// LocalDeviceID returns the local device id via settings + device table.
func (s *SyncStore) LocalDeviceID(ctx context.Context) (string, error) {
	return LocalDeviceID(ctx, s.q)
}

// isServer checks if deviceID belongs to a server device; lookup failure => false.
func (s *SyncStore) isServer(ctx context.Context, deviceID string) bool {
	if deviceID == "" {
		return false
	}
	dev, err := s.q.GetDeviceByID(ctx, deviceID)
	if err != nil {
		return false
	}
	return dev.IsServer == 1
}

// prepareHLCVector computes rowHLC/rowSeq and upserts the sync vector for deviceID.
// It handles both locally-generated (ch.Seq==0, assign next seq) and replayed
// (ch.Seq!=0, advance vector if needed) cases. Caller must hold s.mu or be in
// a transaction's isolated txStore. It ensures HLC is seeded and advances it
// as needed, then upserts sync_vector. Returns the final rowHLC and rowSeq to
// be persisted with the change.
func (s *SyncStore) prepareHLCVector(ctx context.Context, deviceID string, ch SyncChange) (int64, int64, error) {
	s.ensureHLC(ctx)
	var rowHLC int64
	if ch.HLC != 0 {
		s.hlc.Observe(ch.HLC)
		rowHLC = ch.HLC
	} else {
		wall := ch.UpdatedAt
		if wall == 0 {
			wall = time.Now().UnixMilli()
		}
		rowHLC = s.hlc.Tick(wall)
	}
	rowSeq := ch.Seq
	var vectorSeq, vectorHLC int64
	if rowSeq == 0 {
		ns, curHLC, err := s.nextSeqLocked(ctx, deviceID)
		if err != nil {
			return 0, 0, err
		}
		rowSeq = ns
		vectorSeq = ns
		vectorHLC = curHLC
		if vectorHLC < rowHLC {
			vectorHLC = rowHLC
		}
		if vectorHLC < s.hlc.Current() {
			vectorHLC = s.hlc.Current()
		}
		rowHLC = vectorHLC
		if err := s.upsertSyncVector(ctx, db.UpsertSyncVectorParams{DeviceID: deviceID, Seq: vectorSeq, Hlc: vectorHLC}); err != nil {
			return 0, 0, err
		}
	} else {
		curSeq, curHLC, err := s.GetVector(ctx, deviceID)
		if err != nil {
			return 0, 0, err
		}
		if curHLC > s.hlc.Current() {
			s.hlc.Observe(curHLC)
		}
		vectorSeq = curSeq
		if rowSeq > curSeq {
			vectorSeq = rowSeq
		}
		vectorHLC = s.hlc.Current()
		if vectorHLC < curHLC {
			vectorHLC = curHLC
		}
		if vectorHLC < rowHLC {
			vectorHLC = rowHLC
		}
		if err := s.upsertSyncVector(ctx, db.UpsertSyncVectorParams{DeviceID: deviceID, Seq: vectorSeq, Hlc: vectorHLC}); err != nil {
			return 0, 0, err
		}
	}
	return rowHLC, rowSeq, nil
}

func (s *SyncStore) effectivePolicy(ctx context.Context) MergePolicy {
	base := s.policy
	if base == nil {
		base = LWWPolicy{}
	}
	switch p := base.(type) {
	case LWWPolicy:
		if p.IsServer == nil {
			p.IsServer = func(id string) bool { return s.isServer(ctx, id) }
		}
		return p
	case *LWWPolicy:
		if p != nil {
			cp := *p
			if cp.IsServer == nil {
				cp.IsServer = func(id string) bool { return s.isServer(ctx, id) }
			}
			return cp
		}
	}
	return base
}

// Reconcile applies inbound changes per-field LWW with delete-wins and deterministic tie-breakers,
// then returns outbound changes (revision > sinceRev) and new revision.
// Delete-wins: a __deleted tombstone always wins over a concurrent field edit, irrespective of UpdatedAt.
// When both sides are __deleted, LWW decides. Otherwise LWW via MergePolicy.
// Tie on HLC/UpdatedAt -> server wins (deprecated), then deviceId lex order.
// It is atomic when backed by *sql.DB: the inbound loop and cursor advance run in a single transaction.
func (s *SyncStore) Reconcile(ctx context.Context, deviceID string, sinceRev int64, inbound []SyncChange) (outbound []SyncChange, newRev int64, rejected []SyncChange, err error) {
	if len(inbound) > 5000 {
		return nil, 0, nil, fmt.Errorf("too many changes: %d > 5000", len(inbound))
	}
	policy := s.effectivePolicy(ctx)
	// Ensure HLC seeded from DB max before transactional path.
	s.ensureHLC(ctx)
	// Attempt transactional path when we have a *sql.DB.
	if dbq, ok := any(s.q).(*db.Queries); ok {
		if sqlDB, ok := dbq.UnderlyingDB().(*sql.DB); ok {
			tx, err := sqlDB.BeginTx(ctx, nil)
			if err == nil {
				txQ := dbq.WithTx(tx)
				txStore := &SyncStore{q: txQ, policy: s.policy, hlc: s.hlc}
				txPolicy := txStore.effectivePolicy(ctx)
				outbound, newRev, rejected, err = txStore.reconcileInternal(ctx, deviceID, sinceRev, inbound, txPolicy)
				if err != nil {
					_ = tx.Rollback()
					return nil, 0, nil, err
				}
				if err := tx.Commit(); err != nil {
					return nil, 0, nil, err
				}
				return outbound, newRev, rejected, nil
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconcileInternal(ctx, deviceID, sinceRev, inbound, policy)
}

func (s *SyncStore) reconcileInternal(ctx context.Context, deviceID string, sinceRev int64, inbound []SyncChange, policy MergePolicy) (outbound []SyncChange, newRev int64, rejected []SyncChange, err error) {
	// Validate sender exists (defense in depth; p2p handler also validates).
	if deviceID != "" {
		if err := s.ValidateDevice(ctx, deviceID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, 0, nil, fmt.Errorf("unknown device %q: %w", deviceID, err)
			}
			return nil, 0, nil, err
		}
	}
	// Observe inbound HLCs to advance clock (P2P). Single pass before processing.
	for _, inc := range inbound {
		if inc.HLC != 0 {
			s.hlc.Observe(inc.HLC)
		}
	}
	for _, inc := range inbound {
		if inc.DeviceID == "" {
			inc.DeviceID = deviceID
		}
		effectiveID := inc.DeviceID
		if effectiveID == "" {
			effectiveID = deviceID
		}
		// Validate that the claimed author exists; reject forged/unknown devices
		// instead of creating vector rows for arbitrary IDs.
		if err := s.ValidateDevice(ctx, effectiveID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				rejected = append(rejected, inc)
				continue
			}
			return nil, 0, nil, err
		}

		if inc.Field != "__deleted" {
			tomb, terr := s.GetLatestForField(ctx, inc.EntityType, inc.EntityID, "__deleted")
			if terr != nil {
				return nil, 0, nil, terr
			}
			if tomb != nil {
				rejected = append(rejected, inc)
				continue
			}
		}

		existing, err := s.GetLatestForField(ctx, inc.EntityType, inc.EntityID, inc.Field)
		if err != nil {
			return nil, 0, nil, err
		}
		if existing == nil {
			if _, aerr := s.appendChangeLocked(ctx, effectiveID, inc); aerr != nil {
				return nil, 0, nil, aerr
			}
			continue
		}

		isExistingDeleted := existing.Field == "__deleted"
		isIncomingDeleted := inc.Field == "__deleted"

		if isExistingDeleted && !isIncomingDeleted {
			rejected = append(rejected, inc)
			continue
		}
		if !isExistingDeleted && isIncomingDeleted {
			if _, aerr := s.appendChangeLocked(ctx, effectiveID, inc); aerr != nil {
				return nil, 0, nil, aerr
			}
			continue
		}
		if policy.PickWinner(*existing, inc) {
			if _, aerr := s.appendChangeLocked(ctx, effectiveID, inc); aerr != nil {
				return nil, 0, nil, aerr
			}
		} else {
			rejected = append(rejected, inc)
		}
	}

	outbound, err = s.ListSince(ctx, sinceRev, 10000)
	if err != nil {
		return nil, 0, nil, err
	}
	newRev, err = s.GetMaxRevision(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	// Vector is maintained per-change via appendChangeLocked/nextSeqLocked; no global overwriting.
	if cerr := s.SetCursor(ctx, deviceID, newRev); cerr != nil {
		return nil, 0, nil, cerr
	}
	if rejected == nil {
		rejected = []SyncChange{}
	}
	return outbound, newRev, rejected, nil
}
