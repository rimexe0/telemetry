// Package enrollment owns the durable, one-time device pairing boundary.
//
// A collector generates its bearer secret locally and sends only its SHA-256
// digest while starting a session. After the owner approves the session, the
// same secret authorizes ingestion. The core HTTP layer can use the
// registered/authorized distinction to prevent revoked devices falling back to
// legacy global credentials.
package enrollment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultPairingTTL      = 10 * time.Minute
	defaultMaxPending      = 32
	defaultMaxStarts       = 128
	defaultRateWindow      = time.Hour
	defaultPairingIDBytes  = 32
	ownerIdentity          = "owner"
	pairingAdvisoryLockKey = int64(0x74656c656d657472) // "telemetr"
)

var (
	ErrInvalidDeviceID       = errors.New("invalid device id")
	ErrInvalidDeviceName     = errors.New("invalid device name")
	ErrInvalidCredentialHash = errors.New("credential_hash must be 64 hexadecimal characters")
	ErrInvalidPairingID      = errors.New("invalid pairing id")
	ErrInvalidSecret         = errors.New("pairing secret is required")
	ErrAlreadyRegistered     = errors.New("device id is already registered")
	ErrPendingLimit          = errors.New("pending pairing limit reached")
	ErrRateLimited           = errors.New("pairing rate limit reached")
	ErrPairingNotFound       = errors.New("pairing session not found")
	ErrPairingExpired        = errors.New("pairing session expired")
	ErrPairingNotPending     = errors.New("pairing session is not pending")
	ErrUnauthorized          = errors.New("invalid pairing credentials")
	ErrDeviceNotFound        = errors.New("device not found")
	ErrDeviceRevoked         = errors.New("device is revoked")
)

// Options controls bounded pairing state. The defaults are deliberately
// operational limits, not owner behaviour policy.
type Options struct {
	PairingTTL     time.Duration
	MaxPending     int
	MaxStarts      int
	RateWindow     time.Duration
	PairingIDBytes int
	Now            func() time.Time
}

func (o Options) withDefaults() Options {
	if o.PairingTTL <= 0 {
		o.PairingTTL = defaultPairingTTL
	}
	if o.MaxPending <= 0 {
		o.MaxPending = defaultMaxPending
	}
	if o.MaxStarts <= 0 {
		o.MaxStarts = defaultMaxStarts
	}
	if o.RateWindow <= 0 {
		o.RateWindow = defaultRateWindow
	}
	if o.PairingIDBytes < 16 {
		o.PairingIDBytes = defaultPairingIDBytes
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	return o
}

// Service is the PostgreSQL-backed enrollment service.
type Service struct {
	pool *pgxpool.Pool
	opts Options
}

// Open opens an enrollment service. Call Migrate explicitly during deployment.
func Open(ctx context.Context, databaseURL string) (*Service, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("database URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse enrollment PostgreSQL URL: %w", err)
	}
	// Pairing is low-volume and should not consume the memory budget needed by
	// observation ingestion on a small personal server.
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open enrollment PostgreSQL pool: %w", err)
	}
	return New(pool, Options{}), nil
}

// New wraps an existing pool. The pool remains owned by the caller.
func New(pool *pgxpool.Pool, opts Options) *Service {
	return &Service{pool: pool, opts: opts.withDefaults()}
}

// Close releases a pool opened by Open. A service created with New shares the
// pool with its caller and should not call Close.
func (s *Service) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *Service) now() time.Time { return s.opts.Now().UTC() }

// PairingSession is the public state of a pairing session. Credential hashes
// and owner identities are intentionally not included.
type PairingSession struct {
	PairingID  string     `json:"pairing_id"`
	DeviceID   string     `json:"device_id"`
	DeviceName string     `json:"device_name,omitempty"`
	Status     string     `json:"status"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
}

// Device is the owner-facing enrollment record.
type Device struct {
	DeviceID   string     `json:"device_id"`
	DeviceName string     `json:"device_name"`
	PairingID  string     `json:"pairing_id"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// Start retains an expiring request separately from the approved device registry.
func (s *Service) Start(ctx context.Context, deviceID, deviceName string, credentialHash []byte) (PairingSession, error) {
	if err := validateDeviceID(deviceID); err != nil {
		return PairingSession{}, err
	}
	if err := validateDeviceName(deviceName); err != nil {
		return PairingSession{}, err
	}
	if len(credentialHash) != sha256.Size {
		return PairingSession{}, ErrInvalidCredentialHash
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PairingSession{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pairingAdvisoryLockKey); err != nil {
		return PairingSession{}, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telemetry_devices WHERE device_id=$1)`, deviceID).Scan(&exists); err != nil {
		return PairingSession{}, err
	}
	if exists {
		return PairingSession{}, ErrAlreadyRegistered
	}
	now := s.now()
	var value PairingSession
	err = tx.QueryRow(ctx, `SELECT pairing_id,device_id,device_name,status,expires_at,approved_at FROM telemetry_pairing_sessions WHERE device_id=$1 AND credential_hash=$2 AND status='pending' AND expires_at>$3 ORDER BY created_at DESC LIMIT 1`, deviceID, credentialHash, now).Scan(&value.PairingID, &value.DeviceID, &value.DeviceName, &value.Status, &value.ExpiresAt, &value.ApprovedAt)
	if err == nil {
		return value, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PairingSession{}, err
	}
	var pending, recent int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FILTER(WHERE status='pending' AND expires_at>$1),COUNT(*) FILTER(WHERE created_at>=$2) FROM telemetry_pairing_sessions WHERE (status='pending' AND expires_at>$1) OR created_at>=$2`, now, now.Add(-s.opts.RateWindow)).Scan(&pending, &recent); err != nil {
		return PairingSession{}, err
	}
	if pending >= s.opts.MaxPending {
		return PairingSession{}, ErrPendingLimit
	}
	if recent >= s.opts.MaxStarts {
		return PairingSession{}, ErrRateLimited
	}
	id, err := randomID(s.opts.PairingIDBytes)
	if err != nil {
		return PairingSession{}, err
	}
	value = PairingSession{PairingID: id, DeviceID: deviceID, DeviceName: deviceName, Status: "pending", ExpiresAt: now.Add(s.opts.PairingTTL)}
	_, err = tx.Exec(ctx, `INSERT INTO telemetry_pairing_sessions(pairing_id,device_id,device_name,credential_hash,status,created_at,expires_at) VALUES($1,$2,$3,$4,'pending',$5,$6)`, id, deviceID, deviceName, credentialHash, now, value.ExpiresAt)
	if err != nil {
		return PairingSession{}, err
	}
	return value, tx.Commit(ctx)
}

func (s *Service) Poll(ctx context.Context, pairingID, secret string) (PairingSession, error) {
	if secret == "" || len(secret) > 4096 {
		return PairingSession{}, ErrUnauthorized
	}
	var hash []byte
	err := s.pool.QueryRow(ctx, `SELECT credential_hash FROM telemetry_pairing_sessions WHERE pairing_id=$1`, pairingID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return PairingSession{}, ErrUnauthorized
	}
	if err != nil {
		return PairingSession{}, err
	}
	actual := sha256.Sum256([]byte(secret))
	if !equalHash(hash, actual[:]) {
		return PairingSession{}, ErrUnauthorized
	}
	value, err := s.Details(ctx, pairingID)
	if err != nil {
		return value, err
	}
	if value.Status == "expired" {
		return value, ErrPairingExpired
	}
	if value.Status == "revoked" {
		return value, ErrDeviceRevoked
	}
	return value, nil
}

func (s *Service) Details(ctx context.Context, pairingID string) (PairingSession, error) {
	value, err := s.session(ctx, pairingID)
	if err != nil {
		return value, err
	}
	if value.Status == "pending" && !s.now().Before(value.ExpiresAt) {
		value.Status = "expired"
	}
	return value, nil
}

func (s *Service) Approve(ctx context.Context, pairingID string) (PairingSession, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PairingSession{}, err
	}
	defer tx.Rollback(ctx)
	// Serialize approval with starts/revocations so one device can only acquire one credential.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pairingAdvisoryLockKey); err != nil {
		return PairingSession{}, err
	}
	var value PairingSession
	var hash []byte
	err = tx.QueryRow(ctx, `SELECT pairing_id,device_id,device_name,status,expires_at,approved_at,credential_hash FROM telemetry_pairing_sessions WHERE pairing_id=$1 FOR UPDATE`, pairingID).Scan(&value.PairingID, &value.DeviceID, &value.DeviceName, &value.Status, &value.ExpiresAt, &value.ApprovedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, ErrPairingNotFound
	}
	if err != nil {
		return value, err
	}
	var existingPair, status string
	err = tx.QueryRow(ctx, `SELECT pairing_id,status FROM telemetry_devices WHERE device_id=$1`, value.DeviceID).Scan(&existingPair, &status)
	if err == nil {
		if existingPair != pairingID {
			return value, ErrAlreadyRegistered
		}
		if status == "revoked" {
			return value, ErrDeviceRevoked
		}
		value.Status = "approved"
		return value, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return value, err
	}
	if value.Status != "pending" {
		return value, ErrPairingNotPending
	}
	now := s.now()
	if !now.Before(value.ExpiresAt) {
		return value, ErrPairingExpired
	}
	_, err = tx.Exec(ctx, `INSERT INTO telemetry_devices(device_id,device_name,pairing_id,credential_hash,status,owner_id,created_at,approved_at) VALUES($1,$2,$3,$4,'active',$5,$6,$6)`, value.DeviceID, value.DeviceName, pairingID, hash, ownerIdentity, now)
	if err != nil {
		return value, err
	}
	if _, err = tx.Exec(ctx, `UPDATE telemetry_pairing_sessions SET status='approved',approved_at=$2 WHERE pairing_id=$1`, pairingID, now); err != nil {
		return value, err
	}
	value.Status = "approved"
	value.ApprovedAt = &now
	return value, tx.Commit(ctx)
}

func (s *Service) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.pool.Query(ctx, `SELECT device_id,device_name,pairing_id,status,created_at,approved_at,revoked_at,last_seen_at FROM telemetry_devices WHERE owner_id=$1 ORDER BY created_at DESC LIMIT 1000`, ownerIdentity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Device{}
	for rows.Next() {
		var v Device
		if err := rows.Scan(&v.DeviceID, &v.DeviceName, &v.PairingID, &v.Status, &v.CreatedAt, &v.ApprovedAt, &v.RevokedAt, &v.LastSeenAt); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *Service) Revoke(ctx context.Context, deviceID string) (Device, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pairingAdvisoryLockKey); err != nil {
		return Device{}, err
	}
	var v Device
	err = tx.QueryRow(ctx, `UPDATE telemetry_devices SET status='revoked',revoked_at=COALESCE(revoked_at,$2) WHERE device_id=$1 AND owner_id=$3 RETURNING device_id,device_name,pairing_id,status,created_at,approved_at,revoked_at,last_seen_at`, deviceID, s.now(), ownerIdentity).Scan(&v.DeviceID, &v.DeviceName, &v.PairingID, &v.Status, &v.CreatedAt, &v.ApprovedAt, &v.RevokedAt, &v.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrDeviceNotFound
	}
	if err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}

// AuthorizeDevice distinguishes unregistered devices from revoked credentials.
func (s *Service) AuthorizeDevice(ctx context.Context, deviceID, token string) (registered, authorized bool, err error) {
	var hash []byte
	var status string
	err = s.pool.QueryRow(ctx, `SELECT credential_hash,status FROM telemetry_devices WHERE device_id=$1`, deviceID).Scan(&hash, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if token == "" || len(token) > 4096 || status != "active" {
		return true, false, nil
	}
	actual := sha256.Sum256([]byte(token))
	if !equalHash(hash, actual[:]) {
		return true, false, nil
	}
	// Diagnostic write; a matching credential alone does not prove a successful upload.
	_, _ = s.pool.Exec(ctx, `UPDATE telemetry_devices SET last_seen_at=$2 WHERE device_id=$1 AND status='active' AND (last_seen_at IS NULL OR last_seen_at<$2-interval '1 minute')`, deviceID, s.now())
	return true, true, nil
}

func (s *Service) session(ctx context.Context, pairingID string) (PairingSession, error) {
	if pairingID == "" || len(pairingID) > 200 {
		return PairingSession{}, ErrInvalidPairingID
	}
	var v PairingSession
	err := s.pool.QueryRow(ctx, `SELECT p.pairing_id,p.device_id,p.device_name,CASE WHEN d.status='revoked' THEN 'revoked' ELSE p.status END,p.expires_at,p.approved_at FROM telemetry_pairing_sessions p LEFT JOIN telemetry_devices d ON d.pairing_id=p.pairing_id WHERE p.pairing_id=$1`, pairingID).Scan(&v.PairingID, &v.DeviceID, &v.DeviceName, &v.Status, &v.ExpiresAt, &v.ApprovedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrPairingNotFound
	}
	return v, err
}

func validateDeviceID(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > 200 || trimmed != value || strings.ContainsAny(value, "\r\n") {
		return ErrInvalidDeviceID
	}
	return nil
}

func validateDeviceName(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > 200 || trimmed != value || strings.ContainsAny(value, "\r\n") {
		return ErrInvalidDeviceName
	}
	return nil
}

func randomID(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

// ParseCredentialHash validates the wire representation sent by collectors.
func ParseCredentialHash(value string) ([]byte, error) {
	if len(value) != sha256.Size*2 {
		return nil, ErrInvalidCredentialHash
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return nil, ErrInvalidCredentialHash
	}
	return decoded, nil
}

// CredentialHash returns the wire hash for a collector secret.
func CredentialHash(secret string) string {
	hash := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(hash[:])
}

// equalHash is kept private so all credential comparisons use constant time.
func equalHash(left, right []byte) bool { return subtle.ConstantTimeCompare(left, right) == 1 }
