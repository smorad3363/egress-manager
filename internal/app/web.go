package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/egress-manager/egress-manager/internal/api"
	"github.com/egress-manager/egress-manager/internal/auth"
	"github.com/egress-manager/egress-manager/internal/buildinfo"
	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/ipc"
)

type WebOptions struct {
	ConfigPath string
	KeyPath    string
	Logger     *slog.Logger
}

func RunWeb(ctx context.Context, options WebOptions) error {
	if ctx == nil {
		return fmt.Errorf("web context is required")
	}
	configuration, err := config.Load(options.ConfigPath)
	if err != nil {
		return err
	}
	key, err := config.LoadSharedKey(options.KeyPath)
	if err != nil {
		return err
	}
	logger := options.Logger
	if logger == nil {
		logger = NewLogger(slog.LevelInfo)
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
	authentication, err := auth.NewService(store, auth.NewPasswordHasher(), sessions, auth.DefaultThrottlePolicy(), bucketKey)
	if err != nil {
		return err
	}
	ipcAuthenticator, err := ipc.NewAuthenticator(key)
	if err != nil {
		return err
	}
	control := api.IPCControl{Client: ipc.Client{
		SocketPath:    configuration.ControlSocketPath,
		Authenticator: ipcAuthenticator,
		Timeout:       3 * time.Second,
	}}
	health := api.HealthHandler{
		Version: buildinfo.Version,
		Checks: []api.HealthCheck{
			{Name: "database", Critical: true, Check: databaseConnection.PingContext},
			{Name: "egressd", Critical: true, Check: control.Health},
		},
		Timeout: 4 * time.Second,
	}
	apiServer, err := api.NewServer(api.ServerConfig{
		SessionCookieName: configuration.SessionCookieName,
		SecureCookies:     true,
	}, logger, authentication, sessions, control, health)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              net.JoinHostPort(configuration.ListenAddress, fmt.Sprintf("%d", configuration.ListenPort)),
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	serveResult := make(chan error, 1)
	go func() {
		logger.Info("egress-web ready", "address", httpServer.Addr, "version", buildinfo.Version)
		if configuration.TLSCertificatePath != "" {
			serveResult <- httpServer.ListenAndServeTLS(configuration.TLSCertificatePath, configuration.TLSPrivateKeyPath)
			return
		}
		serveResult <- httpServer.ListenAndServe()
	}()

	select {
	case serveErr := <-serveResult:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve web API: %w", serveErr)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shutdown web API: %w", err)
		}
		serveErr := <-serveResult
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve web API: %w", serveErr)
		}
		return nil
	}
}
