package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/egress-manager/egress-manager/internal/auth"
	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
)

func ProvisionAdmin(ctx context.Context, configPath, keyPath, username, password string) error {
	configuration, err := config.Load(configPath)
	if err != nil {
		return err
	}
	key, err := config.LoadSharedKey(keyPath)
	if err != nil {
		return err
	}
	databaseConnection, err := database.Open(ctx, configuration.DatabasePath)
	if err != nil {
		return err
	}
	defer databaseConnection.Close()
	store := database.NewStore(databaseConnection)
	sessions, err := auth.NewSessionManager(store, 24*time.Hour)
	if err != nil {
		return err
	}
	bucketKey, err := deriveAuthenticationKey(key)
	if err != nil {
		return err
	}
	service, err := auth.NewService(store, auth.NewPasswordHasher(), sessions, auth.DefaultThrottlePolicy(), bucketKey)
	if err != nil {
		return err
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return fmt.Errorf("generate admin ID: %w", err)
	}
	return service.ProvisionAdmin(ctx, domain.ID("admin_"+hex.EncodeToString(idBytes)), username, password, time.Now().UTC())
}
