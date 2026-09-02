package sync

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

	// signer signs changes this device authors; localDeviceID says which
	// author ID that key speaks for. Both are empty until SetSigner runs, in
	// which case changes go out unsigned and peers will only accept them
	// directly from us.
	signer        ed25519.PrivateKey
	localDeviceID string

	// materializer projects accepted inbound changes onto the domain tables
	// they describe. The change log is the source of truth; materialization is
	// the readable copy, so it runs AFTER the log commits and a failure there
	// is logged rather than rolling the change back.
	materializer Materializer

	// projections carries accepted batches to the background projector used by
	// the network reconcile paths. It is FIFO and single-consumer, so batches
	// project in the order their rounds committed.
	projections chan []SyncChange
	projectOnce sync.Once
}

// Materializer applies a change that has been accepted into the log onto the
// table it describes (a rename onto track_override, a crop onto track_crop).
// Without one, changes replicate but never become visible.
type Materializer interface {
	Apply(ctx context.Context, ch SyncChange) error
}

// SetMaterializer installs the projection applied to accepted inbound changes.
// Optional: a store without one still replicates, it just does not surface
// what it received.
func (s *SyncStore) SetMaterializer(m Materializer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.materializer = m
}

// SetSigner installs the key used to sign locally-authored changes. deviceID
// must be this node's own device ID: it is covered by the signature, so a
// mismatch would produce signatures no peer can verify.
func (s *SyncStore) SetSigner(priv ed25519.PrivateKey, deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.signer = priv
	s.localDeviceID = deviceID
}

// signerFor returns the signing key when deviceID is this node's own identity.
func (s *SyncStore) signerFor(deviceID string) ed25519.PrivateKey {
	if s.localDeviceID == "" || deviceID != s.localDeviceID {
		return nil
	}
	return s.signer
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
	// A signed change carries the exact bytes its signature covers; re-encoding
	// Value could produce different JSON and invalidate the signature.
	if ch.ValueJSON != "" {
		return ch.ValueJSON, nil
	}
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
		ValueJSON:  row.ValueJson,
		Sig:        row.Sig,
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

func (s *SyncStore) listUnsignedForDevice(ctx context.Context, deviceID string) ([]db.SyncChange, error) {
	if qq, ok := any(s.q).(interface {
		ListUnsignedSyncChangesForDevice(context.Context, string) ([]db.SyncChange, error)
	}); ok {
		return qq.ListUnsignedSyncChangesForDevice(ctx, deviceID)
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.ListUnsignedSyncChangesForDevice(ctx, deviceID)
	}
	return nil, fmt.Errorf("querier does not support ListUnsignedSyncChangesForDevice")
}

func (s *SyncStore) updateSig(ctx context.Context, rev int64, sig string) error {
	if qq, ok := any(s.q).(interface {
		UpdateSyncChangeSig(context.Context, db.UpdateSyncChangeSigParams) error
	}); ok {
		return qq.UpdateSyncChangeSig(ctx, db.UpdateSyncChangeSigParams{Revision: rev, Sig: sig})
	}
	if dbq, ok := any(s.q).(*db.Queries); ok {
		return dbq.UpdateSyncChangeSig(ctx, db.UpdateSyncChangeSigParams{Revision: rev, Sig: sig})
	}
	return fmt.Errorf("querier does not support UpdateSyncChangeSig")
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

// BackfillLocalSignatures signs any locally-authored rows that were stored
// with an empty signature (pre-migration or while the signer was unavailable
// due to identity failure). Without this, those rows are unrelayable: a third
// peer treats sig=="" as ErrBadSignature and drops them, causing silent
// convergence failure. The backfill is idempotent and safe to run on every
// boot after SetSigner.
func (s *SyncStore) BackfillLocalSignatures(ctx context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	priv := s.signer
	deviceID := s.localDeviceID
	if priv == nil || deviceID == "" {
		return 0, nil
	}
	rows, err := s.listUnsignedForDevice(ctx, deviceID)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, r := range rows {
		sig := SignChange(priv, r.DeviceID, r.EntityType, r.EntityID, r.Field, r.ValueJson, r.UpdatedAt, r.Hlc, r.Seq)
		if sig == "" {
			continue
		}
		if err := s.updateSig(ctx, r.Revision, sig); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
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

// signatureFor returns the signature to persist with a change: the author's
// own signature when relaying someone else's change, or a fresh one when this
// device is the author. Signing happens after HLC/seq assignment because both
// are covered by the signature.
func (s *SyncStore) signatureFor(deviceID string, ch SyncChange, valueJSON string, rowHLC, rowSeq int64) string {
	if ch.Sig != "" {
		return ch.Sig
	}
	priv := s.signerFor(deviceID)
	if priv == nil {
		return ""
	}
	return SignChange(priv, deviceID, ch.EntityType, ch.EntityID, ch.Field, valueJSON, ch.UpdatedAt, rowHLC, rowSeq)
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
		Sig:        s.signatureFor(deviceID, ch, valueJSON, rowHLC, rowSeq),
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
		Sig:        s.signatureFor(deviceID, ch, valueJSON, rowHLC, rowSeq),
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
// log exceeds the outbound cap. Empty vector means no filter (send all).
func (s *SyncStore) ListSinceVector(ctx context.Context, vector map[string]int64, limit int64) ([]SyncChange, error) {
	if limit <= 0 {
		limit = 10000
	}
	const batch = 1000
	var out []SyncChange
	var cursor int64
	for int64(len(out)) < limit {
		fetch := int64(batch)
		if fetch > 10000 {
			fetch = 10000
		}
		rows, err := s.ListSince(ctx, cursor, fetch)
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
		if int64(len(rows)) < fetch {
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
		// rowHLC is left exactly as the peer sent it. The signature covers the
		// HLC, so raising it to the local clock would make the row fail
		// verification the moment this device relays it onward, and would make
		// the author's own later edit look older than the copy stored here --
		// the two devices would then disagree forever, each resending its
		// version. Only the vector row advances.
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

// materializeTimeout bounds one batch's projection. It is generous: the work is
// local writes, and the alternative to finishing is a row the log holds but no
// screen ever shows.
const materializeTimeout = 10 * time.Minute

// MaxReconcileBatch is the largest inbound batch one Reconcile call accepts.
// Both sync producers page well above it, so callers that hand over whatever a
// peer sent must go through ReconcileBatched.
const MaxReconcileBatch = 5000

// ReconcileBatched applies inbound in slices of at most MaxReconcileBatch and
// returns the last slice's outbound and revision plus every rejection.
//
// A peer that authored more than one batch's worth -- a first pairing after
// BackfillHistory publishes a play per listen, say -- sends them all in one
// round. Refusing the round would append nothing, leave the vector where it
// was, and produce the identical oversized round on the next tick forever, so
// the batch is split rather than declined. Slices commit one at a time; a
// failure part-way leaves the earlier ones applied, which the next round
// resumes from, since every change is idempotent per field.
func (s *SyncStore) ReconcileBatched(ctx context.Context, deviceID string, sinceRev int64, inbound []SyncChange) (outbound []SyncChange, newRev int64, rejected []SyncChange, err error) {
	return s.reconcileBatched(ctx, deviceID, sinceRev, inbound, false)
}

// ReconcileBatchedAsync is ReconcileBatched with the projection handed to a
// background worker, so the caller returns as soon as the log has committed.
//
// A sync round runs under a short network deadline while the projection writes
// through the domain services -- minutes of work on a first sync after
// BackfillHistory. Projecting before replying spends the peer's deadline, so
// the peer gives up and resends the whole batch next round. Batches project in
// the order they committed, and the queue blocks rather than drops when the
// projector falls behind.
func (s *SyncStore) ReconcileBatchedAsync(ctx context.Context, deviceID string, sinceRev int64, inbound []SyncChange) (outbound []SyncChange, newRev int64, rejected []SyncChange, err error) {
	return s.reconcileBatched(ctx, deviceID, sinceRev, inbound, true)
}

func (s *SyncStore) reconcileBatched(ctx context.Context, deviceID string, sinceRev int64, inbound []SyncChange, deferProjection bool) (outbound []SyncChange, newRev int64, rejected []SyncChange, err error) {
	// Splitting an oversized batch must not split a catalog entity away from
	// the rows that name it: everything keyed on a catalog id is unprojectable
	// -- a play violates the plays.catalog_id foreign key, a rename parks under
	// an id nothing resolves -- until the entity exists. Hoisting them puts
	// every entity in the first slice.
	inbound = catalogFirst(inbound)
	for start := 0; ; start += MaxReconcileBatch {
		end := start + MaxReconcileBatch
		if end > len(inbound) {
			end = len(inbound)
		}
		out, rev, rej, rerr := s.reconcile(ctx, deviceID, sinceRev, inbound[start:end], deferProjection)
		if rerr != nil {
			return nil, 0, nil, rerr
		}
		outbound, newRev = out, rev
		rejected = append(rejected, rej...)
		if end >= len(inbound) {
			return outbound, newRev, rejected, nil
		}
	}
}

// catalogFirst returns changes reordered so catalog entities come first,
// stable within each group. Entities carry the identity every other change
// addresses a track by, so they have to be applied ahead of the rest.
func catalogFirst(changes []SyncChange) []SyncChange {
	ordered := make([]SyncChange, 0, len(changes))
	for _, ch := range changes {
		if ch.EntityType == EntityCatalog {
			ordered = append(ordered, ch)
		}
	}
	if len(ordered) == 0 || len(ordered) == len(changes) {
		return changes
	}
	for _, ch := range changes {
		if ch.EntityType != EntityCatalog {
			ordered = append(ordered, ch)
		}
	}
	return ordered
}

// NoOutbound is a sinceRev that asks Reconcile for no outbound changes. A
// caller that pulls separately -- the p2p syncer does -- would otherwise pay
// for a full change-log read per round only to discard it.
const NoOutbound int64 = -1

// Reconcile applies inbound changes per-field LWW with delete-wins and deterministic tie-breakers,
// then returns outbound changes (revision > sinceRev) and new revision.
// Delete-wins: a __deleted tombstone always wins over a concurrent field edit, irrespective of UpdatedAt.
// When both sides are __deleted, LWW decides. Otherwise LWW via MergePolicy.
// Tie on HLC/UpdatedAt -> server wins (deprecated), then deviceId lex order.
// It is atomic when backed by *sql.DB: the inbound loop and cursor advance run in a single transaction.
// Pass NoOutbound as sinceRev to skip computing outbound entirely.
func (s *SyncStore) Reconcile(ctx context.Context, deviceID string, sinceRev int64, inbound []SyncChange) (outbound []SyncChange, newRev int64, rejected []SyncChange, err error) {
	return s.reconcile(ctx, deviceID, sinceRev, inbound, false)
}

func (s *SyncStore) reconcile(ctx context.Context, deviceID string, sinceRev int64, inbound []SyncChange, deferProjection bool) (outbound []SyncChange, newRev int64, rejected []SyncChange, err error) {
	if len(inbound) > MaxReconcileBatch {
		return nil, 0, nil, fmt.Errorf("too many changes: %d > %d", len(inbound), MaxReconcileBatch)
	}
	// Projection runs after the lock is released. It writes through the domain
	// services, which is minutes of work on a first sync after BackfillHistory,
	// and every local write -- a play, a rename, a playlist edit -- takes the
	// same mutex. Holding it across materialize would stall the whole app for
	// as long as the projection ran.
	outbound, newRev, rejected, accepted, err := s.reconcileLocked(ctx, deviceID, sinceRev, inbound)
	if err != nil {
		return nil, 0, nil, err
	}
	if deferProjection {
		s.enqueueProjection(accepted)
	} else {
		s.materialize(ctx, accepted)
	}
	return outbound, newRev, rejected, nil
}

// enqueueProjection hands an accepted batch to the background projector,
// starting it on first use. The send blocks when the projector is behind: a
// dropped batch would never be resent, since the log has already committed and
// the vector has already advanced.
func (s *SyncStore) enqueueProjection(accepted []SyncChange) {
	if len(accepted) == 0 {
		return
	}
	s.projectOnce.Do(func() {
		s.projections = make(chan []SyncChange, 32)
		go func() {
			for batch := range s.projections {
				s.materialize(context.Background(), batch)
			}
		}()
	})
	s.projections <- accepted
}

func (s *SyncStore) reconcileLocked(ctx context.Context, deviceID string, sinceRev int64, inbound []SyncChange) (outbound []SyncChange, newRev int64, rejected []SyncChange, accepted []SyncChange, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
				outbound, newRev, rejected, accepted, err = txStore.reconcileInternal(ctx, deviceID, sinceRev, inbound, txPolicy)
				if err != nil {
					_ = tx.Rollback()
					return nil, 0, nil, nil, err
				}
				if err := tx.Commit(); err != nil {
					return nil, 0, nil, nil, err
				}
				return outbound, newRev, rejected, accepted, nil
			}
		}
	}
	return s.reconcileInternal(ctx, deviceID, sinceRev, inbound, policy)
}

// materialize projects accepted changes onto the tables they describe. It runs
// after the log has committed: the log is the source of truth, so a projection
// failure is logged and left for the next change to correct rather than
// discarding a change every peer has already accepted.
func (s *SyncStore) materialize(ctx context.Context, accepted []SyncChange) {
	s.mu.Lock()
	m := s.materializer
	s.mu.Unlock()
	if m == nil || len(accepted) == 0 {
		return
	}
	// Projection outlives the caller's deadline. A sync round runs under a
	// short network timeout; the log commits inside it, and the local vector
	// advances, so no peer ever resends these rows. If every Apply then failed
	// with the round's context error -- which is exactly what a first sync
	// after BackfillHistory does, committing thousands of plays with the
	// deadline already spent -- those changes would be permanently invisible
	// on this device. Cancellation of the round must not reach here.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), materializeTimeout)
	defer cancel()
	// Catalog entities first: a play or a rename names a track by its catalog
	// id, so the entity has to exist before the row that points at it.
	for _, ch := range catalogFirst(accepted) {
		if err := m.Apply(ctx, ch); err != nil {
			log.Printf("sync: could not apply %s/%s %s: %v", ch.EntityType, ch.EntityID, ch.Field, err)
		}
	}
}

func (s *SyncStore) reconcileInternal(ctx context.Context, deviceID string, sinceRev int64, inbound []SyncChange, policy MergePolicy) (outbound []SyncChange, newRev int64, rejected []SyncChange, accepted []SyncChange, err error) {
	// Validate sender exists (defense in depth; p2p handler also validates).
	if deviceID != "" {
		if err := s.ValidateDevice(ctx, deviceID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, 0, nil, nil, fmt.Errorf("unknown device %q: %w", deviceID, err)
			}
			return nil, 0, nil, nil, err
		}
	}
	// Refuse changes whose HLC is too far ahead of local wall time before
	// anything observes or stores them. A stored row keeps the HLC the peer
	// sent -- it is covered by the signature, so it cannot be rewritten on the
	// way in -- and PickWinner compares stored HLCs, so accepting one row from
	// a device with a broken clock would let it win every later conflict on
	// that field permanently. Rejecting costs that one edit; accepting costs
	// the field.
	s.ensureHLC(ctx)
	kept := make([]SyncChange, 0, len(inbound))
	var drifted int
	for _, inc := range inbound {
		// A change with no HLC is a legacy row: PickWinner ranks it by
		// UpdatedAt, so that is the clock the bound has to be applied to.
		clock := inc.HLC
		if clock == 0 {
			clock = inc.UpdatedAt
		}
		if clock != 0 && !s.hlc.withinDrift(clock) {
			rejected = append(rejected, inc)
			drifted++
			continue
		}
		kept = append(kept, inc)
	}
	if drifted > 0 {
		// Say so loudly: the edits are dropped, and the cause is a clock on the
		// sending device rather than anything the user did here.
		log.Printf("WARNING: sync: refused %d change(s) from %q dated more than %s ahead of "+
			"local time; check that device's clock", drifted, deviceID, maxHLCDrift)
	}
	inbound = kept
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
			return nil, 0, nil, nil, err
		}

		if inc.Field != "__deleted" {
			tomb, terr := s.GetLatestForField(ctx, inc.EntityType, inc.EntityID, "__deleted")
			if terr != nil {
				return nil, 0, nil, nil, terr
			}
			if tomb != nil {
				rejected = append(rejected, inc)
				continue
			}
		}

		existing, err := s.GetLatestForField(ctx, inc.EntityType, inc.EntityID, inc.Field)
		if err != nil {
			return nil, 0, nil, nil, err
		}
		if existing == nil {
			if _, aerr := s.appendChangeLocked(ctx, effectiveID, inc); aerr != nil {
				return nil, 0, nil, nil, aerr
			}
			accepted = append(accepted, inc)
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
				return nil, 0, nil, nil, aerr
			}
			accepted = append(accepted, inc)
			continue
		}
		if policy.PickWinner(*existing, inc) {
			if _, aerr := s.appendChangeLocked(ctx, effectiveID, inc); aerr != nil {
				return nil, 0, nil, nil, aerr
			}
			accepted = append(accepted, inc)
		} else {
			rejected = append(rejected, inc)
		}
	}

	if sinceRev >= 0 {
		outbound, err = s.ListSince(ctx, sinceRev, 10000)
		if err != nil {
			return nil, 0, nil, nil, err
		}
	}
	newRev, err = s.GetMaxRevision(ctx)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	// Vector is maintained per-change via appendChangeLocked/nextSeqLocked; no global overwriting.
	if cerr := s.SetCursor(ctx, deviceID, newRev); cerr != nil {
		return nil, 0, nil, nil, cerr
	}
	if rejected == nil {
		rejected = []SyncChange{}
	}
	return outbound, newRev, rejected, accepted, nil
}
