package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrLoginThrottled     = errors.New("login throttled")
	usernamePattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)
)

type Repository interface {
	SessionRepository
	CreateAdmin(context.Context, database.Admin) error
	AdminByUsername(context.Context, string) (database.Admin, error)
	RecordLoginAttempt(context.Context, [32]byte, bool, time.Time) error
	CountRecentFailedAttempts(context.Context, [32]byte, time.Time) (int, error)
}

type ThrottlePolicy struct {
	Window      time.Duration
	MaxFailures int
}

func DefaultThrottlePolicy() ThrottlePolicy {
	return ThrottlePolicy{Window: 15 * time.Minute, MaxFailures: 5}
}

func (policy ThrottlePolicy) Validate() error {
	if policy.Window < time.Minute || policy.Window > 24*time.Hour {
		return fmt.Errorf("login throttle window must be between 1 minute and 24 hours")
	}
	if policy.MaxFailures < 1 || policy.MaxFailures > 100 {
		return fmt.Errorf("login throttle maximum must be between 1 and 100")
	}
	return nil
}

type Service struct {
	repository Repository
	hasher     PasswordHasher
	sessions   *SessionManager
	policy     ThrottlePolicy
	bucketKey  [32]byte
	dummyHash  string
	loginMu    sync.Mutex
}

func NewService(repository Repository, hasher PasswordHasher, sessions *SessionManager, policy ThrottlePolicy, bucketKey [32]byte) (*Service, error) {
	if repository == nil || sessions == nil {
		return nil, fmt.Errorf("authentication repository and session manager are required")
	}
	if err := hasher.Params.Validate(); err != nil {
		return nil, err
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if bucketKey == [32]byte{} {
		return nil, fmt.Errorf("login throttle bucket key must not be zero")
	}
	dummyHash, err := hasher.Hash("dummy-password-never-valid")
	if err != nil {
		return nil, fmt.Errorf("create timing-equalization hash: %w", err)
	}
	return &Service{
		repository: repository,
		hasher:     hasher,
		sessions:   sessions,
		policy:     policy,
		bucketKey:  bucketKey,
		dummyHash:  dummyHash,
	}, nil
}

func NormalizeUsername(username string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(normalized) {
		return "", fmt.Errorf("username must contain 3 to 64 lowercase letters, digits, dots, underscores, or hyphens")
	}
	return normalized, nil
}

func (service *Service) ProvisionAdmin(ctx context.Context, id domain.ID, username, password string, now time.Time) error {
	if err := id.Validate("admin id"); err != nil {
		return err
	}
	normalized, err := NormalizeUsername(username)
	if err != nil {
		return err
	}
	passwordHash, err := service.hasher.Hash(password)
	if err != nil {
		return err
	}
	now = now.UTC().Truncate(time.Second)
	return service.repository.CreateAdmin(ctx, database.Admin{
		ID:           string(id),
		Username:     normalized,
		PasswordHash: passwordHash,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
}

func (service *Service) Login(ctx context.Context, username, password string, clientAddress netip.Addr, now time.Time) (SessionTokens, error) {
	if !clientAddress.IsValid() {
		return SessionTokens{}, ErrInvalidCredentials
	}
	normalized, normalizeErr := NormalizeUsername(username)
	if normalizeErr != nil {
		normalized = "invalid-user"
	}
	bucket := service.loginBucket(normalized, clientAddress)
	now = now.UTC()

	service.loginMu.Lock()
	defer service.loginMu.Unlock()

	failures, err := service.repository.CountRecentFailedAttempts(ctx, bucket, now.Add(-service.policy.Window))
	if err != nil {
		return SessionTokens{}, fmt.Errorf("read login throttle state: %w", err)
	}
	if failures >= service.policy.MaxFailures {
		return SessionTokens{}, ErrLoginThrottled
	}

	admin, lookupErr := service.repository.AdminByUsername(ctx, normalized)
	passwordHash := service.dummyHash
	if lookupErr == nil {
		passwordHash = admin.PasswordHash
	} else if !errors.Is(lookupErr, database.ErrNotFound) {
		return SessionTokens{}, fmt.Errorf("read login account: %w", lookupErr)
	}
	passwordMatches, verifyErr := service.hasher.Verify(passwordHash, password)
	if verifyErr != nil {
		return SessionTokens{}, fmt.Errorf("verify login password: %w", verifyErr)
	}
	valid := normalizeErr == nil && lookupErr == nil && passwordMatches
	if err := service.repository.RecordLoginAttempt(ctx, bucket, valid, now); err != nil {
		return SessionTokens{}, fmt.Errorf("record login result: %w", err)
	}
	if !valid {
		return SessionTokens{}, ErrInvalidCredentials
	}
	return service.sessions.Create(ctx, admin.ID, now)
}

func (service *Service) loginBucket(username string, clientAddress netip.Addr) [32]byte {
	mac := hmac.New(sha256.New, service.bucketKey[:])
	_, _ = mac.Write([]byte(username))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(clientAddress.String()))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
