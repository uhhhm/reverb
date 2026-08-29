package sync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"sort"

	"github.com/google/uuid"
	"github.com/uhhhm/reverb/internal/store/db"
)

// Device mirrors the device table but with typed IsServer bool for convenience.
type Device struct {
	ID        string
	Name      string
	TokenHash string
	IsServer  bool
	CreatedAt int64
	LastSeen  int64
}

// PairingCode mirrors the pairing_code table.
type PairingCode struct {
	Code           string
	ExpiresAt      int64
	UsedAt         *int64
	UsedByDeviceID *string
}

// Querier is the minimal store seam used by the pairing service.
// db.Queries satisfies it.
type Querier interface {
	CreateDevice(ctx context.Context, arg db.CreateDeviceParams) error
	GetDeviceByID(ctx context.Context, id string) (db.Device, error)
	GetDeviceByTokenHash(ctx context.Context, tokenHash string) (db.Device, error)
	ListDevices(ctx context.Context) ([]db.Device, error)
	CreatePairingCode(ctx context.Context, arg db.CreatePairingCodeParams) error
	GetPairingCode(ctx context.Context, code string) (db.PairingCode, error)
	MarkPairingCodeUsed(ctx context.Context, arg db.MarkPairingCodeUsedParams) error
	DeleteExpiredPairingCodes(ctx context.Context) error
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, arg db.UpsertSettingParams) error
	TouchDeviceLastSeen(ctx context.Context, id string) error
}

const serverDeviceIDKey = "server_device_id"

const localDeviceIDKey = "local_device_id"

// ServerDeviceQuerier is the minimal store seam for server-device resolution.
// *db.Queries satisfies it, as do the api OfflineSetStore/PairingStore aliases.
type ServerDeviceQuerier interface {
	GetSetting(ctx context.Context, key string) (string, error)
	GetDeviceByID(ctx context.Context, id string) (db.Device, error)
	ListDevices(ctx context.Context) ([]db.Device, error)
}

// ServerDeviceID returns the server device id (is_server=1) using the
// settings key server_device_id with a ListDevices fallback. Single canonical
// implementation for all callers; api helpers delegate here.
func ServerDeviceID(ctx context.Context, q ServerDeviceQuerier) (string, error) {
	if q == nil {
		return "", ErrNoServerDevice
	}
	if id, err := q.GetSetting(ctx, serverDeviceIDKey); err == nil && id != "" {
		if dev, err := q.GetDeviceByID(ctx, id); err == nil {
			return dev.ID, nil
		}
	}
	devices, err := q.ListDevices(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range devices {
		if d.IsServer == 1 {
			return d.ID, nil
		}
	}
	return "", ErrNoServerDevice
}

func generateToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plain := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(plain))
	hash := hex.EncodeToString(h[:])
	return plain, hash, nil
}

func tokenHash(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}

// EnsureLocalDevice ensures this installation has a stable local device identity
// (local_device_id in settings + device row). It is the P2P replacement for
// EnsureServerDevice: every node is a peer, is_server is ignored. It creates
// dev_<uuid> with IsServer=0 if none exists.
func EnsureLocalDevice(ctx context.Context, q Querier) (string, error) {
	if id, err := q.GetSetting(ctx, localDeviceIDKey); err == nil && id != "" {
		if _, err := q.GetDeviceByID(ctx, id); err == nil {
			return id, nil
		}
	}
	devices, err := q.ListDevices(ctx)
	if err != nil {
		return "", err
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].CreatedAt == devices[j].CreatedAt {
			return devices[i].ID < devices[j].ID
		}
		return devices[i].CreatedAt < devices[j].CreatedAt
	})
	// Prefer an existing non-server device whose token we can use; otherwise create.
	for _, d := range devices {
		if d.IsServer == 0 {
			_ = q.UpsertSetting(ctx, db.UpsertSettingParams{Key: localDeviceIDKey, Value: d.ID})
			return d.ID, nil
		}
	}
	// No peer device yet — create one. Reuse EnsureServerDevice's race handling
	// but with is_server=0 so the partial unique index does not fire.
	plain, hash, err := generateToken()
	if err != nil {
		return "", err
	}
	_ = plain
	id := "dev_" + uuid.NewString()
	hostname := "local"
	if err := q.CreateDevice(ctx, db.CreateDeviceParams{
		ID:        id,
		Name:      hostname,
		TokenHash: hash,
		IsServer:  0,
	}); err != nil {
		// Race: another caller created a peer device concurrently — re-list.
		devices2, lerr := q.ListDevices(ctx)
		if lerr == nil {
			sort.Slice(devices2, func(i, j int) bool {
				if devices2[i].CreatedAt == devices2[j].CreatedAt {
					return devices2[i].ID < devices2[j].ID
				}
				return devices2[i].CreatedAt < devices2[j].CreatedAt
			})
			for _, d := range devices2 {
				if d.IsServer == 0 {
					_ = q.UpsertSetting(ctx, db.UpsertSettingParams{Key: localDeviceIDKey, Value: d.ID})
					return d.ID, nil
				}
			}
		}
		return "", err
	}
	_ = q.UpsertSetting(ctx, db.UpsertSettingParams{Key: localDeviceIDKey, Value: id})
	return id, nil
}

// LocalDeviceID returns the local device id (settings local_device_id with fallback to any peer device).
func LocalDeviceID(ctx context.Context, q ServerDeviceQuerier) (string, error) {
	if q == nil {
		return "", ErrNoServerDevice
	}
	if id, err := q.GetSetting(ctx, localDeviceIDKey); err == nil && id != "" {
		if dev, err := q.GetDeviceByID(ctx, id); err == nil {
			return dev.ID, nil
		}
	}
	devices, err := q.ListDevices(ctx)
	if err != nil {
		return "", err
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].CreatedAt == devices[j].CreatedAt {
			return devices[i].ID < devices[j].ID
		}
		return devices[i].CreatedAt < devices[j].CreatedAt
	})
	for _, d := range devices {
		if d.IsServer == 0 {
			return d.ID, nil
		}
	}
	// Back-compat: fall back to server device if no peer exists yet.
	for _, d := range devices {
		if d.IsServer == 1 {
			return d.ID, nil
		}
	}
	return "", ErrNoServerDevice
}

// AuthorDeviceID returns the device identity changes authored on this node must
// be attributed to: the local device, falling back to the server device only if
// there is none.
//
// This must be the local device, not the server device. The local device is the
// one bound to the libp2p identity, so it is the only one that has a signing key
// (SetSigner installs it for that ID and signerFor refuses every other) and the
// only one whose public key peers learn during pairing. A change authored under
// the server device can never be signed and can never be verified by a peer: it
// arrives naming an author the peer has no key for, is refused as unverifiable,
// and is resent on every anti-entropy round forever.
func AuthorDeviceID(ctx context.Context, q ServerDeviceQuerier) (string, error) {
	if id, err := LocalDeviceID(ctx, q); err == nil && id != "" {
		return id, nil
	}
	return ServerDeviceID(ctx, q)
}

// EnsureServerDevice ensures one device row with is_server=1 exists.
// If none exists it creates dev_<uuid> name="server" with a random token hash.
// It stores server_device_id in settings for convenience and is idempotent.
// It is safe under concurrent callers: a partial unique index guarantees at most
// one is_server=1 row, and a transaction serializes the check-then-create.
func EnsureServerDevice(ctx context.Context, q Querier) (string, error) {
	// Ensure the single-server invariant at the DB level when we have a *sql.DB.
	if dbq, ok := q.(*db.Queries); ok {
		if sqlDB, ok := dbq.UnderlyingDB().(*sql.DB); ok {
			_, _ = sqlDB.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_device_single_server ON device(is_server) WHERE is_server = 1`)
			tx, err := sqlDB.BeginTx(ctx, nil)
			if err == nil {
				txQ := dbq.WithTx(tx)
				devices, err := txQ.ListDevices(ctx)
				if err == nil {
					for _, d := range devices {
						if d.IsServer == 1 {
							_ = txQ.UpsertSetting(ctx, db.UpsertSettingParams{Key: serverDeviceIDKey, Value: d.ID})
							_ = tx.Commit()
							return d.ID, nil
						}
					}
				}
				plain, hash, gerr := generateToken()
				if gerr == nil {
					_ = plain
					id := "dev_" + uuid.NewString()
					if cerr := txQ.CreateDevice(ctx, db.CreateDeviceParams{
						ID:        id,
						Name:      "server",
						TokenHash: hash,
						IsServer:  1,
					}); cerr == nil {
						_ = txQ.UpsertSetting(ctx, db.UpsertSettingParams{Key: serverDeviceIDKey, Value: id})
						if err := tx.Commit(); err == nil {
							return id, nil
						}
					} else {
						_ = tx.Rollback()
						// Likely unique constraint violation from concurrent winner; fall through to re-check.
						devices2, lerr := q.ListDevices(ctx)
						if lerr == nil {
							for _, d := range devices2 {
								if d.IsServer == 1 {
									_ = q.UpsertSetting(ctx, db.UpsertSettingParams{Key: serverDeviceIDKey, Value: d.ID})
									return d.ID, nil
								}
							}
						}
						return "", cerr
					}
				}
				_ = tx.Rollback()
			}
		}
	}
	devices, err := q.ListDevices(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range devices {
		if d.IsServer == 1 {
			_ = q.UpsertSetting(ctx, db.UpsertSettingParams{Key: serverDeviceIDKey, Value: d.ID})
			return d.ID, nil
		}
	}
	plain, hash, err := generateToken()
	if err != nil {
		return "", err
	}
	_ = plain
	id := "dev_" + uuid.NewString()
	if err := q.CreateDevice(ctx, db.CreateDeviceParams{
		ID:        id,
		Name:      "server",
		TokenHash: hash,
		IsServer:  1,
	}); err != nil {
		// Race: another caller may have created the server device concurrently.
		// Re-check list and return existing if now present.
		devices2, lerr := q.ListDevices(ctx)
		if lerr == nil {
			for _, d := range devices2 {
				if d.IsServer == 1 {
					_ = q.UpsertSetting(ctx, db.UpsertSettingParams{Key: serverDeviceIDKey, Value: d.ID})
					return d.ID, nil
				}
			}
		}
		return "", err
	}
	_ = q.UpsertSetting(ctx, db.UpsertSettingParams{Key: serverDeviceIDKey, Value: id})
	return id, nil
}
