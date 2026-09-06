package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/api"
	"github.com/egress-manager/egress-manager/internal/auth"
	"github.com/egress-manager/egress-manager/internal/buildinfo"
	"github.com/egress-manager/egress-manager/internal/config"
	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/ipc"
	"github.com/egress-manager/egress-manager/internal/secrets"
)

const defaultWebAssetsPath = "/usr/local/lib/egress-manager/web"

type WebOptions struct {
	ConfigPath string
	KeyPath    string
	AssetsPath string
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
	protector, err := secrets.NewProtector(key, nil)
	if err != nil {
		return err
	}
	store, err := database.NewProtectedStore(databaseConnection, protector)
	if err != nil {
		return err
	}
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
		Timeout:       25 * time.Second,
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
	}, logger, authentication, sessions, control, store, store, store, store, store, health)
	if err != nil {
		return err
	}

	handler := apiServer.Handler()
	assetsPath := options.AssetsPath
	if assetsPath == "" {
		assetsPath = defaultWebAssetsPath
		if _, statErr := os.Stat(filepath.Join(assetsPath, "index.html")); os.IsNotExist(statErr) {
			assetsPath = ""
		}
	}
	if assetsPath != "" {
		panel, panelErr := panelHandler(handler, assetsPath)
		if panelErr != nil {
			return panelErr
		}
		handler = api.SecurityHeaders(panel)
	}

	httpServer := &http.Server{
		Addr:              net.JoinHostPort(configuration.ListenAddress, fmt.Sprintf("%d", configuration.ListenPort)),
		Handler:           handler,
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

func panelHandler(apiHandler http.Handler, assetsPath string) (http.Handler, error) {
	if apiHandler == nil {
		return nil, fmt.Errorf("panel API handler is required")
	}
	root, err := filepath.Abs(assetsPath)
	if err != nil {
		return nil, fmt.Errorf("resolve web assets path: %w", err)
	}
	indexPath := filepath.Join(root, "index.html")
	info, err := os.Stat(indexPath)
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return nil, fmt.Errorf("web assets are incomplete at %s: %w", indexPath, err)
	}
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api" || strings.HasPrefix(request.URL.Path, "/api/") {
			apiHandler.ServeHTTP(writer, request)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			http.Error(writer, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}

		cleanPath := path.Clean("/" + request.URL.Path)
		candidate := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(cleanPath, "/")))
		if candidateInfo, statErr := os.Stat(candidate); statErr == nil && (candidateInfo.Mode().IsRegular() || candidateInfo.IsDir()) {
			files.ServeHTTP(writer, request)
			return
		}

		fallback := request.Clone(request.Context())
		fallbackURL := *request.URL
		fallbackURL.Path = "/"
		fallbackURL.RawPath = ""
		fallback.URL = &fallbackURL
		files.ServeHTTP(writer, fallback)
	}), nil
}
