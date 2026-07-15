package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrEmailExists    = errors.New("email already exists")
	ErrInvalidRefresh = errors.New("invalid refresh token")
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Session struct {
	UserID   string
	DeviceID string
}

func (s *Store) Register(
	ctx context.Context,
	email, passwordHash, deviceID, deviceName string,
	refreshHash []byte,
	refreshExpiresAt time.Time,
) (User, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	userID := uuid.NewString()
	if deviceID == "" {
		deviceID = uuid.NewString()
	}
	if _, err := uuid.Parse(deviceID); err != nil {
		return User{}, "", fmt.Errorf("invalid device id: %w", err)
	}
	now := time.Now().UTC()
	user := User{ID: userID, Email: strings.ToLower(strings.TrimSpace(email)), CreatedAt: now}
	_, err = tx.Exec(ctx, `
		INSERT INTO users(id, email, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)`, user.ID, user.Email, passwordHash, now)
	if isUniqueViolation(err) {
		return User{}, "", ErrEmailExists
	}
	if err != nil {
		return User{}, "", fmt.Errorf("insert user: %w", err)
	}
	if err := createDeviceAndRefreshToken(
		ctx, tx, user.ID, deviceID, deviceName, refreshHash, refreshExpiresAt,
	); err != nil {
		return User{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, "", err
	}
	return user, deviceID, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, email, password_hash, created_at
		FROM users WHERE email = lower($1)`, strings.TrimSpace(email),
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, email, password_hash, created_at
		FROM users WHERE id = $1`, id,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) CreateSession(
	ctx context.Context,
	userID, deviceID, deviceName string,
	refreshHash []byte,
	refreshExpiresAt time.Time,
) (string, error) {
	if deviceID == "" {
		deviceID = uuid.NewString()
	}
	if _, err := uuid.Parse(deviceID); err != nil {
		return "", fmt.Errorf("invalid device id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := createDeviceAndRefreshToken(
		ctx, tx, userID, deviceID, deviceName, refreshHash, refreshExpiresAt,
	); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return deviceID, nil
}

func createDeviceAndRefreshToken(
	ctx context.Context,
	tx pgx.Tx,
	userID, deviceID, deviceName string,
	refreshHash []byte,
	refreshExpiresAt time.Time,
) error {
	deviceName = strings.TrimSpace(deviceName)
	if deviceName == "" {
		deviceName = "Unknown device"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO devices(id, user_id, name)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE
		SET name = EXCLUDED.name, last_seen_at = now()
		WHERE devices.user_id = EXCLUDED.user_id`, deviceID, userID, deviceName); err != nil {
		return fmt.Errorf("upsert device: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens(id, user_id, device_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		uuid.NewString(), userID, deviceID, refreshHash, refreshExpiresAt,
	); err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (s *Store) RotateRefreshToken(
	ctx context.Context,
	oldHash, newHash []byte,
	newExpiresAt time.Time,
) (Session, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var session Session
	var tokenID string
	err = tx.QueryRow(ctx, `
		SELECT id::text, user_id::text, device_id::text
		FROM refresh_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		FOR UPDATE`, oldHash,
	).Scan(&tokenID, &session.UserID, &session.DeviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidRefresh
	}
	if err != nil {
		return Session{}, err
	}
	if _, err := tx.Exec(ctx, "UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1", tokenID); err != nil {
		return Session{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens(id, user_id, device_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		uuid.NewString(), session.UserID, session.DeviceID, newHash, newExpiresAt,
	); err != nil {
		return Session{}, err
	}
	if _, err := tx.Exec(ctx, "UPDATE devices SET last_seen_at = now() WHERE id = $1", session.DeviceID); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *Store) RevokeRefreshToken(ctx context.Context, hash []byte) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = COALESCE(revoked_at, now())
		WHERE token_hash = $1`, hash)
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
