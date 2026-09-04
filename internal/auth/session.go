package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
)

const secretBytes = 32

var (
	ErrInvalidSession = errors.New("session is invalid")
	ErrInvalidCSRF    = errors.New("CSRF token is invalid")
)

type SessionRepository interface {
	CreateSession(context.Context, database.Session) error
	ActiveSession(context.Context, [32]byte, time.Time) (database.Session, error)
	RevokeSession(context.Context, [32]byte, time.Time) error
}

type SessionTokens struct {
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
}

type SessionManager struct {
	repository SessionRepository
	random     io.Reader
	lifetime   time.Duration
}

func NewSessionManager(repository SessionRepository, lifetime time.Duration) (*SessionManager, error) {
	if repository == nil {
		return nil, fmt.Errorf("session repository is required")
	}
	if lifetime < 5*time.Minute || lifetime > 7*24*time.Hour {
		return nil, fmt.Errorf("session lifetime must be between 5 minutes and 7 days")
	}
	return &SessionManager{repository: repository, random: rand.Reader, lifetime: lifetime}, nil
}

func (manager *SessionManager) Create(ctx context.Context, adminID string, now time.Time) (SessionTokens, error) {
	sessionToken, tokenHash, err := manager.newSecret()
	if err != nil {
		return SessionTokens{}, err
	}
	csrfToken, csrfHash, err := manager.newSecret()
	if err != nil {
		return SessionTokens{}, err
	}
	now = now.UTC()
	expiresAt := now.Add(manager.lifetime)
	if err := manager.repository.CreateSession(ctx, database.Session{
		TokenHash:  tokenHash,
		AdminID:    adminID,
		CSRFHash:   csrfHash,
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
		LastSeenAt: now,
	}); err != nil {
		return SessionTokens{}, fmt.Errorf("persist session: %w", err)
	}
	return SessionTokens{SessionToken: sessionToken, CSRFToken: csrfToken, ExpiresAt: expiresAt}, nil
}

func (manager *SessionManager) Authenticate(ctx context.Context, encodedToken string, now time.Time) (database.Session, error) {
	hash, err := hashEncodedSecret(encodedToken)
	if err != nil {
		return database.Session{}, ErrInvalidSession
	}
	session, err := manager.repository.ActiveSession(ctx, hash, now.UTC())
	if errors.Is(err, database.ErrNotFound) {
		return database.Session{}, ErrInvalidSession
	}
	if err != nil {
		return database.Session{}, fmt.Errorf("read session: %w", err)
	}
	return session, nil
}

func (manager *SessionManager) VerifyCSRF(session database.Session, encodedToken string) error {
	hash, err := hashEncodedSecret(encodedToken)
	if err != nil || subtle.ConstantTimeCompare(hash[:], session.CSRFHash[:]) != 1 {
		return ErrInvalidCSRF
	}
	return nil
}

func (manager *SessionManager) Revoke(ctx context.Context, encodedToken string, now time.Time) error {
	hash, err := hashEncodedSecret(encodedToken)
	if err != nil {
		return ErrInvalidSession
	}
	if err := manager.repository.RevokeSession(ctx, hash, now.UTC()); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return ErrInvalidSession
		}
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (manager *SessionManager) newSecret() (string, [32]byte, error) {
	secret := make([]byte, secretBytes)
	if _, err := io.ReadFull(manager.random, secret); err != nil {
		return "", [32]byte{}, fmt.Errorf("read session randomness: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(secret)
	return encoded, sha256.Sum256(secret), nil
}

func hashEncodedSecret(encoded string) ([32]byte, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) != secretBytes {
		return [32]byte{}, fmt.Errorf("invalid secret encoding")
	}
	return sha256.Sum256(decoded), nil
}
