package sync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"github.com/google/uuid"
	"github.com/maxjb-xyz/reverb/internal/store/db"
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
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, arg db.UpsertSettingParams) error
	TouchDeviceLastSeen(ctx context.Context, id string) error
}

const serverDeviceIDKey = "server_device_id"

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

// EnsureServerDevice ensures one device row with is_server=1 exists.
// If none exists it creates dev_<uuid> name="server" with a random token hash.
// It stores server_device_id in settings for convenience and is idempotent.
func EnsureServerDevice(ctx context.Context, q Querier) (string, error) {
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
