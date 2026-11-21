package enrollment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Setup codes are made by the signed-in owner in the panel. Typing or
// scanning one on a new device connects it without a second approval step,
// because making the code was the approval. A code works once and expires.

const (
	codeAlphabet       = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // Crockford base32: no I, L, O, U
	codeLength         = 8
	codeFailureWindow  = 10 * time.Minute
	codeFailureAllowed = 20
)

// Invite is the owner-facing record of a setup code. Code is only set when
// the invite is created; the server keeps a hash.
type Invite struct {
	InviteID     string     `json:"invite_id"`
	Code         string     `json:"code,omitempty"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	UsedAt       *time.Time `json:"used_at,omitempty"`
	UsedByDevice string     `json:"used_by_device,omitempty"`
	UsedByName   string     `json:"used_by_name,omitempty"`
}

// NormalizeCode accepts a code as people type it: any case, with dashes or
// spaces, and O, I or L where 0 or 1 was meant.
func NormalizeCode(value string) (string, bool) {
	var builder strings.Builder
	for _, r := range strings.ToUpper(value) {
		switch {
		case r == '-' || r == ' ':
			continue
		case r == 'O':
			r = '0'
		case r == 'I' || r == 'L':
			r = '1'
		}
		if !strings.ContainsRune(codeAlphabet, r) {
			return "", false
		}
		builder.WriteRune(r)
	}
	normalized := builder.String()
	return normalized, len(normalized) == codeLength
}

// FormatCode shows a normalized code in two groups of four.
func FormatCode(code string) string {
	if len(code) != codeLength {
		return code
	}
	return code[:4] + "-" + code[4:]
}

func newCode() (string, error) {
	random := make([]byte, codeLength)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	code := make([]byte, codeLength)
	for i, b := range random {
		// 256 is a multiple of 32, so every character is equally likely.
		code[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	return string(code), nil
}

func codeHash(normalized string) []byte {
	hash := sha256.Sum256([]byte("telemetry-setup-code:" + normalized))
	return hash[:]
}

// codeGuessesExceeded reports whether too many wrong codes arrived lately.
func (s *Service) codeGuessesExceeded(now time.Time) bool {
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	kept := s.codeFailures[:0]
	for _, at := range s.codeFailures {
		if now.Sub(at) < codeFailureWindow {
			kept = append(kept, at)
		}
	}
	s.codeFailures = kept
	return len(kept) >= codeFailureAllowed
}

func (s *Service) noteWrongCode(now time.Time) {
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	s.codeFailures = append(s.codeFailures, now)
}

// CreateInvite makes a setup code for one new device.
func (s *Service) CreateInvite(ctx context.Context) (Invite, error) {
	now := s.now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invite{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pairingAdvisoryLockKey); err != nil {
		return Invite{}, err
	}
	var open int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM telemetry_pairing_invites WHERE used_at IS NULL AND cancelled_at IS NULL AND expires_at>$1`, now).Scan(&open); err != nil {
		return Invite{}, err
	}
	if open >= s.opts.MaxOpenInvites {
		return Invite{}, ErrInviteLimit
	}
	id, err := randomID(16)
	if err != nil {
		return Invite{}, err
	}
	code, err := newCode()
	if err != nil {
		return Invite{}, err
	}
	invite := Invite{InviteID: id, Code: FormatCode(code), Status: "open", CreatedAt: now, ExpiresAt: now.Add(s.opts.InviteTTL)}
	if _, err = tx.Exec(ctx, `INSERT INTO telemetry_pairing_invites(invite_id,code_hash,created_at,expires_at) VALUES($1,$2,$3,$4)`, id, codeHash(code), now, invite.ExpiresAt); err != nil {
		return Invite{}, err
	}
	return invite, tx.Commit(ctx)
}

// ListInvites returns the setup codes of the last day, newest first.
func (s *Service) ListInvites(ctx context.Context) ([]Invite, error) {
	now := s.now()
	rows, err := s.pool.Query(ctx, `SELECT invite_id,created_at,expires_at,used_at,COALESCE(used_by_device,''),COALESCE(used_by_name,''),cancelled_at FROM telemetry_pairing_invites WHERE created_at>$1 ORDER BY created_at DESC LIMIT 20`, now.Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Invite{}
	for rows.Next() {
		var v Invite
		var cancelled *time.Time
		if err := rows.Scan(&v.InviteID, &v.CreatedAt, &v.ExpiresAt, &v.UsedAt, &v.UsedByDevice, &v.UsedByName, &cancelled); err != nil {
			return nil, err
		}
		switch {
		case v.UsedAt != nil:
			v.Status = "used"
		case cancelled != nil:
			v.Status = "cancelled"
		case !now.Before(v.ExpiresAt):
			v.Status = "expired"
		default:
			v.Status = "open"
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// CancelInvite stops an unused setup code from working.
func (s *Service) CancelInvite(ctx context.Context, inviteID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE telemetry_pairing_invites SET cancelled_at=$2 WHERE invite_id=$1 AND used_at IS NULL AND cancelled_at IS NULL`, inviteID, s.now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInviteNotFound
	}
	return nil
}

// redeem connects a device with a setup code inside Start's transaction.
func (s *Service) redeem(ctx context.Context, tx pgx.Tx, deviceID, deviceName string, credentialHash []byte, code string, now time.Time) (PairingSession, error) {
	var value PairingSession
	// The answer to an earlier try with this same key was lost: the device
	// is already connected with it, so say so again.
	err := tx.QueryRow(ctx, `SELECT p.pairing_id,d.device_id,p.device_name,p.expires_at,p.approved_at FROM telemetry_pairing_sessions p JOIN telemetry_devices d ON d.pairing_id=p.pairing_id WHERE p.device_id=$1 AND p.credential_hash=$2 AND d.status='active'`, deviceID, credentialHash).Scan(&value.PairingID, &value.DeviceID, &value.DeviceName, &value.ExpiresAt, &value.ApprovedAt)
	if err == nil {
		value.Status = "approved"
		return value, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PairingSession{}, err
	}
	if s.codeGuessesExceeded(now) {
		return PairingSession{}, ErrRateLimited
	}
	normalized, ok := NormalizeCode(code)
	if !ok {
		s.noteWrongCode(now)
		return PairingSession{}, ErrInvalidCode
	}
	var inviteID string
	err = tx.QueryRow(ctx, `SELECT invite_id FROM telemetry_pairing_invites WHERE code_hash=$1 AND used_at IS NULL AND cancelled_at IS NULL AND expires_at>$2 FOR UPDATE`, codeHash(normalized), now).Scan(&inviteID)
	if errors.Is(err, pgx.ErrNoRows) {
		s.noteWrongCode(now)
		return PairingSession{}, ErrInvalidCode
	}
	if err != nil {
		return PairingSession{}, err
	}
	id, err := randomID(s.opts.PairingIDBytes)
	if err != nil {
		return PairingSession{}, err
	}
	value = PairingSession{PairingID: id, DeviceID: deviceID, DeviceName: deviceName, Status: "approved", ExpiresAt: now.Add(s.opts.PairingTTL), ApprovedAt: &now}
	if _, err = tx.Exec(ctx, `INSERT INTO telemetry_pairing_sessions(pairing_id,device_id,device_name,credential_hash,status,created_at,expires_at,approved_at) VALUES($1,$2,$3,$4,'approved',$5,$6,$5)`, id, deviceID, deviceName, credentialHash, now, value.ExpiresAt); err != nil {
		return PairingSession{}, err
	}
	if value.DeviceID, err = register(ctx, tx, value, credentialHash, now); err != nil {
		return PairingSession{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE telemetry_pairing_invites SET used_at=$2,used_by_device=$3,used_by_name=$4 WHERE invite_id=$1`, inviteID, now, deviceID, deviceName); err != nil {
		return PairingSession{}, err
	}
	return value, tx.Commit(ctx)
}

// phoneQuietFor is how long a phone must have been silent before a new
// install with the same name is taken to be the same phone, set up again.
const phoneQuietFor = 10 * time.Minute

// continued returns the device a new Android install continues: a phone of
// the same name that has gone quiet, as after a format. Its ID changes with
// every format, but the owner has one phone, so its history stays together.
// Anything else keeps its own ID.
func continued(ctx context.Context, tx pgx.Tx, deviceID, deviceName string, now time.Time) (string, error) {
	if !strings.HasPrefix(deviceID, "android-") {
		return deviceID, nil
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telemetry_devices WHERE device_id=$1)`, deviceID).Scan(&exists); err != nil || exists {
		return deviceID, err
	}
	var previous string
	err := tx.QueryRow(ctx, `SELECT device_id FROM telemetry_devices WHERE device_id LIKE 'android-%' AND device_name=$1 AND owner_id=$2 AND (last_seen_at IS NULL OR last_seen_at<$3) ORDER BY COALESCE(last_seen_at,approved_at) DESC LIMIT 1`, deviceName, ownerIdentity, now.Add(-phoneQuietFor)).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return deviceID, nil
	}
	return previous, err
}

// register makes the approved session's key the device's only key. A device
// that was connected before keeps its ID and history; the key it replaced is
// recorded. A phone set up again after a format continues as the same phone:
// the ID it is registered under is returned, and the phone adopts it.
func register(ctx context.Context, tx pgx.Tx, value PairingSession, hash []byte, now time.Time) (string, error) {
	deviceID, err := continued(ctx, tx, value.DeviceID, value.DeviceName, now)
	if err != nil {
		return "", err
	}
	value.DeviceID = deviceID
	return deviceID, registerAs(ctx, tx, value, hash, now)
}

func registerAs(ctx context.Context, tx pgx.Tx, value PairingSession, hash []byte, now time.Time) error {
	var previousPair, previousStatus, previousName string
	err := tx.QueryRow(ctx, `SELECT pairing_id,status,device_name FROM telemetry_devices WHERE device_id=$1 FOR UPDATE`, value.DeviceID).Scan(&previousPair, &previousStatus, &previousName)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO telemetry_devices(device_id,device_name,pairing_id,credential_hash,status,owner_id,created_at,approved_at) VALUES($1,$2,$3,$4,'active',$5,$6,$6)`, value.DeviceID, value.DeviceName, value.PairingID, hash, ownerIdentity, now)
		return err
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO telemetry_device_credential_history(device_id,previous_pairing_id,previous_status,previous_name,replaced_by_pairing_id,replaced_at) VALUES($1,$2,$3,$4,$5,$6)`, value.DeviceID, previousPair, previousStatus, previousName, value.PairingID, now); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE telemetry_devices SET device_name=$2,pairing_id=$3,credential_hash=$4,status='active',approved_at=$5,revoked_at=NULL WHERE device_id=$1`, value.DeviceID, value.DeviceName, value.PairingID, hash, now)
	return err
}

// Removed reports whether this device was connected with this key and then
// removed in the panel, so the device can say so instead of failing quietly.
func (s *Service) Removed(ctx context.Context, deviceID, token string) bool {
	if token == "" || len(token) > 4096 {
		return false
	}
	var hash []byte
	var status string
	if err := s.pool.QueryRow(ctx, `SELECT credential_hash,status FROM telemetry_devices WHERE device_id=$1`, deviceID).Scan(&hash, &status); err != nil {
		return false
	}
	actual := sha256.Sum256([]byte(token))
	return status == "revoked" && equalHash(hash, actual[:])
}

// RelayURL is the relay address setup codes carry, if the owner set one.
func (s *Service) RelayURL(ctx context.Context) (string, error) {
	var value string
	err := s.pool.QueryRow(ctx, `SELECT relay_url FROM telemetry_pairing_settings WHERE id=1`).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetRelayURL saves the relay address, or clears it when empty.
func (s *Service) SetRelayURL(ctx context.Context, value string) error {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value != "" {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return ErrInvalidRelayURL
		}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO telemetry_pairing_settings(id,relay_url,updated_at) VALUES(1,$1,$2) ON CONFLICT(id) DO UPDATE SET relay_url=EXCLUDED.relay_url,updated_at=EXCLUDED.updated_at`, value, s.now())
	return err
}
