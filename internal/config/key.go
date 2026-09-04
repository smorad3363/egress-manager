package config

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func GenerateSharedKey(random io.Reader) (string, error) {
	if random == nil {
		return "", fmt.Errorf("shared key randomness source is required")
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(random, key); err != nil {
		return "", fmt.Errorf("read shared key randomness: %w", err)
	}
	return hex.EncodeToString(key), nil
}

func LoadSharedKey(path string) ([32]byte, error) {
	if !filepath.IsAbs(path) {
		return [32]byte{}, fmt.Errorf("shared key path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return [32]byte{}, fmt.Errorf("inspect shared key: %w", err)
	}
	if !info.Mode().IsRegular() {
		return [32]byte{}, fmt.Errorf("shared key must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o007 != 0 {
		return [32]byte{}, fmt.Errorf("shared key must not be accessible by other users")
	}
	if info.Size() > 128 {
		return [32]byte{}, fmt.Errorf("shared key file is too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return [32]byte{}, fmt.Errorf("read shared key: %w", err)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(decoded) != 32 {
		return [32]byte{}, fmt.Errorf("shared key must contain exactly 32 hex-encoded bytes")
	}
	var key [32]byte
	copy(key[:], decoded)
	if key == [32]byte{} {
		return [32]byte{}, fmt.Errorf("shared key must not be zero")
	}
	return key, nil
}
