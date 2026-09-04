// Package auth implements operator credentials, sessions, CSRF, and login throttling.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	minimumPasswordBytes  = 12
	maximumPasswordBytes  = 1024
	maximumArgonMemoryKiB = 1 << 20
	maximumArgonTime      = 10
	maximumArgonThreads   = 16
)

var ErrInvalidPassword = errors.New("password is invalid")

type Argon2Params struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
	SaltBytes uint32
	KeyBytes  uint32
}

func DefaultArgon2Params() Argon2Params {
	return Argon2Params{
		MemoryKiB: 64 * 1024,
		Time:      3,
		Threads:   4,
		SaltBytes: 16,
		KeyBytes:  32,
	}
}

func (params Argon2Params) Validate() error {
	if params.MemoryKiB < 64 || params.MemoryKiB > maximumArgonMemoryKiB {
		return fmt.Errorf("Argon2 memory must be between 64 KiB and %d KiB", maximumArgonMemoryKiB)
	}
	if params.Time < 1 || params.Time > maximumArgonTime {
		return fmt.Errorf("Argon2 time must be between 1 and %d", maximumArgonTime)
	}
	if params.Threads < 1 || params.Threads > maximumArgonThreads {
		return fmt.Errorf("Argon2 threads must be between 1 and %d", maximumArgonThreads)
	}
	if params.SaltBytes < 16 || params.SaltBytes > 64 {
		return fmt.Errorf("Argon2 salt length must be between 16 and 64 bytes")
	}
	if params.KeyBytes < 16 || params.KeyBytes > 64 {
		return fmt.Errorf("Argon2 key length must be between 16 and 64 bytes")
	}
	return nil
}

type PasswordHasher struct {
	Params Argon2Params
	Random io.Reader
}

func NewPasswordHasher() PasswordHasher {
	return PasswordHasher{Params: DefaultArgon2Params(), Random: rand.Reader}
}

func (hasher PasswordHasher) Hash(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	if err := hasher.Params.Validate(); err != nil {
		return "", err
	}
	if hasher.Random == nil {
		return "", fmt.Errorf("password salt source is missing")
	}
	salt := make([]byte, hasher.Params.SaltBytes)
	if _, err := io.ReadFull(hasher.Random, salt); err != nil {
		return "", fmt.Errorf("read password salt: %w", err)
	}
	digest := argon2.IDKey([]byte(password), salt, hasher.Params.Time, hasher.Params.MemoryKiB, hasher.Params.Threads, hasher.Params.KeyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		hasher.Params.MemoryKiB,
		hasher.Params.Time,
		hasher.Params.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

func (hasher PasswordHasher) Verify(encoded, password string) (bool, error) {
	if len(password) > maximumPasswordBytes {
		return false, ErrInvalidPassword
	}
	params, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.Time, params.MemoryKiB, params.Threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func validatePassword(password string) error {
	if len(password) < minimumPasswordBytes || len(password) > maximumPasswordBytes {
		return ErrInvalidPassword
	}
	return nil
}

func parsePasswordHash(encoded string) (Argon2Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Argon2Params{}, nil, nil, fmt.Errorf("invalid Argon2id hash format")
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return Argon2Params{}, nil, nil, fmt.Errorf("parse Argon2id parameters: %w", err)
	}
	if parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", memory, iterations, threads) {
		return Argon2Params{}, nil, nil, fmt.Errorf("invalid Argon2id parameters")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return Argon2Params{}, nil, nil, fmt.Errorf("decode Argon2id salt: %w", err)
	}
	digest, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return Argon2Params{}, nil, nil, fmt.Errorf("decode Argon2id digest: %w", err)
	}
	params := Argon2Params{
		MemoryKiB: memory,
		Time:      iterations,
		Threads:   threads,
		SaltBytes: uint32(len(salt)),
		KeyBytes:  uint32(len(digest)),
	}
	if err := params.Validate(); err != nil {
		return Argon2Params{}, nil, nil, fmt.Errorf("validate Argon2id parameters: %w", err)
	}
	return params, salt, digest, nil
}
