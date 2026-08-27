package sync

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	gosync "sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/uhhhm/reverb/internal/store/db"
)

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var (
	ErrCodeInvalid  = errors.New("invalid pairing code")
	ErrCodeExpired  = errors.New("pairing code expired")
	ErrCodeUsed     = errors.New("pairing code already used")
	ErrInvalidToken = errors.New("invalid sync token")
)

// fallbackMu serializes the non-atomic Redeem fallback (queriers without
// TryMarkPairingCodeUsed, e.g. test mocks) to prevent TOCTOU double-redeem.
// Production queriers (*db.Queries) always hit the atomic TryMark paths above.
var fallbackMu gosync.Mutex

// PairingService implements pairing code generation and redeem flow.
type PairingService struct {
	q Querier
}

// NewPairingService creates a new pairing service backed by q.
func NewPairingService(q Querier) *PairingService {
	return &PairingService{q: q}
}

func generateStrippedCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 8)
	for i, v := range b {
		out[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	return string(out), nil
}

func normalizeCode(raw string) string {
	var sb strings.Builder
	sb.Grow(len(raw))
	for _, r := range raw {
		if r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		sb.WriteRune(unicode.ToUpper(r))
	}
	return sb.String()
}

func formatCode(stripped string) string {
	return stripped[:4] + "-" + stripped[4:]
}

// GenerateCode creates a new single-use pairing code with 10 minute TTL.
// Code is formatted XXXX-XXXX for display but stored stripped (no dash).
func (s *PairingService) GenerateCode(ctx context.Context) (string, int64, error) {
	_ = s.q.DeleteExpiredPairingCodes(ctx)
	expiresAt := time.Now().Add(10 * time.Minute).Unix()
	// Retry on PK collision (extremely unlikely but correct).
	for attempt := 0; attempt < 5; attempt++ {
		stripped, err := generateStrippedCode()
		if err != nil {
			return "", 0, err
		}
		err = s.q.CreatePairingCode(ctx, db.CreatePairingCodeParams{
			Code:      stripped,
			ExpiresAt: expiresAt,
		})
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "unique") {
				continue
			}
			return "", 0, err
		}
		return formatCode(stripped), expiresAt, nil
	}
	return "", 0, errors.New("failed to generate unique pairing code")
}

// Redeem validates rawCode, checks expiry and single-use, creates a new device row
// with a fresh sync token, marks the code used, and returns deviceID + plain token.
// It is atomic: when the backing store is *db.Queries on *sql.DB it runs in a
// single transaction (create device + conditional claim), preventing TOCTOU
// double-redeem and FK violations (used_by_device_id references device).
func (s *PairingService) Redeem(ctx context.Context, rawCode, deviceName string) (string, string, error) {
	normalized := normalizeCode(rawCode)
	if len(normalized) != 8 {
		return "", "", ErrCodeInvalid
	}
	for _, c := range normalized {
		if !strings.ContainsRune(codeAlphabet, c) {
			return "", "", ErrCodeInvalid
		}
	}
	plain, hash, err := generateToken()
	if err != nil {
		return "", "", err
	}
	deviceID := "dev_" + uuid.NewString()
	// Transactional path when we have a *db.Queries on *sql.DB.
	if dbq, ok := s.q.(*db.Queries); ok {
		if sqlDB, ok := dbq.UnderlyingDB().(*sql.DB); ok {
			tx, err := sqlDB.BeginTx(ctx, nil)
			if err == nil {
				txQ := dbq.WithTx(tx)
				// Create device first so FK on pairing_code is satisfied.
				if err := txQ.CreateDevice(ctx, db.CreateDeviceParams{
					ID:        deviceID,
					Name:      deviceName,
					TokenHash: hash,
					IsServer:  0,
				}); err != nil {
					_ = tx.Rollback()
					return "", "", err
				}
				rows, err := txQ.TryMarkPairingCodeUsed(ctx, db.TryMarkPairingCodeUsedParams{
					Code: normalized,
					UsedByDeviceID: sql.NullString{
						String: deviceID,
						Valid:  true,
					},
				})
				if err != nil {
					_ = tx.Rollback()
					return "", "", err
				}
				if rows == 0 {
					_ = tx.Rollback()
					pc, gerr := s.q.GetPairingCode(ctx, normalized)
					if gerr != nil {
						if errors.Is(gerr, sql.ErrNoRows) {
							return "", "", ErrCodeInvalid
						}
						return "", "", gerr
					}
					if pc.UsedAt.Valid {
						return "", "", ErrCodeUsed
					}
					if time.Now().Unix() > pc.ExpiresAt {
						return "", "", ErrCodeExpired
					}
					return "", "", ErrCodeInvalid
				}
				if err := tx.Commit(); err != nil {
					return "", "", err
				}
				return deviceID, plain, nil
			}
		}
	}
	// Fallback for queriers without TryMark (e.g., mocks) or when tx not available: try conditional claim first if available.
	if tryQ, ok := s.q.(interface {
		TryMarkPairingCodeUsed(context.Context, db.TryMarkPairingCodeUsedParams) (int64, error)
	}); ok {
		// Need device to exist for FK, so create a temporary device then claim, but we can't tx.
		// Instead do non-transactional but in order: create then claim, and if claim fails delete device.
		if err := s.q.CreateDevice(ctx, db.CreateDeviceParams{
			ID:        deviceID,
			Name:      deviceName,
			TokenHash: hash,
			IsServer:  0,
		}); err != nil {
			return "", "", err
		}
		rows, err := tryQ.TryMarkPairingCodeUsed(ctx, db.TryMarkPairingCodeUsedParams{
			Code: normalized,
			UsedByDeviceID: sql.NullString{
				String: deviceID,
				Valid:  true,
			},
		})
		if err != nil {
			// best-effort cleanup
			if del, ok := any(s.q).(interface {
				DeleteDevice(context.Context, string) error
			}); ok {
				_ = del.DeleteDevice(ctx, deviceID)
			}
			return "", "", err
		}
		if rows == 0 {
			if del, ok := any(s.q).(interface {
				DeleteDevice(context.Context, string) error
			}); ok {
				_ = del.DeleteDevice(ctx, deviceID)
			}
			pc, gerr := s.q.GetPairingCode(ctx, normalized)
			if gerr != nil {
				if errors.Is(gerr, sql.ErrNoRows) {
					return "", "", ErrCodeInvalid
				}
				return "", "", gerr
			}
			if pc.UsedAt.Valid {
				return "", "", ErrCodeUsed
			}
			if time.Now().Unix() > pc.ExpiresAt {
				return "", "", ErrCodeExpired
			}
			return "", "", ErrCodeInvalid
		}
		return deviceID, plain, nil
	}
	// Fallback for queriers without TryMark (e.g., mocks): old non-atomic path.
	// This path is test-only in production (*db.Queries always has TryMark) — serialize
	// with fallbackMu to prevent TOCTOU double-redeem, and clean up orphan device on error.
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	pc, err := s.q.GetPairingCode(ctx, normalized)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", ErrCodeInvalid
		}
		return "", "", err
	}
	if pc.UsedAt.Valid {
		return "", "", ErrCodeUsed
	}
	if time.Now().Unix() > pc.ExpiresAt {
		return "", "", ErrCodeExpired
	}
	if err := s.q.CreateDevice(ctx, db.CreateDeviceParams{
		ID:        deviceID,
		Name:      deviceName,
		TokenHash: hash,
		IsServer:  0,
	}); err != nil {
		return "", "", err
	}
	if err := s.q.MarkPairingCodeUsed(ctx, db.MarkPairingCodeUsedParams{
		Code: normalized,
		UsedByDeviceID: sql.NullString{
			String: deviceID,
			Valid:  true,
		},
	}); err != nil {
		if del, ok := any(s.q).(interface {
			DeleteDevice(context.Context, string) error
		}); ok {
			_ = del.DeleteDevice(ctx, deviceID)
		}
		return "", "", err
	}
	return deviceID, plain, nil
}

// AuthenticateByToken hashes the plain token, looks up the device by token_hash,
// touches last_seen, and returns the device or ErrInvalidToken.
func (s *PairingService) AuthenticateByToken(ctx context.Context, token string) (*db.Device, error) {
	hash := tokenHash(token)
	dev, err := s.q.GetDeviceByTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	_ = s.q.TouchDeviceLastSeen(ctx, dev.ID)
	// Re-read to return updated last_seen where possible, but return original on error.
	updated, err := s.q.GetDeviceByID(ctx, dev.ID)
	if err == nil {
		dev = updated
	}
	return &dev, nil
}
