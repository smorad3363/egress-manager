package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/auth"
	"github.com/egress-manager/egress-manager/internal/database"
)

const (
	CSRFHeader       = "X-CSRF-Token"
	maximumJSONBytes = 16 << 10
)

type LoginService interface {
	Login(context.Context, string, string, netip.Addr, time.Time) (auth.SessionTokens, error)
}

type SessionService interface {
	Authenticate(context.Context, string, time.Time) (database.Session, error)
	VerifyCSRF(database.Session, string) error
	Revoke(context.Context, string, time.Time) error
}

type ControlService interface {
	Health(context.Context) error
}

type ServerConfig struct {
	SessionCookieName string
	SecureCookies     bool
}

type Server struct {
	config   ServerConfig
	logger   *slog.Logger
	login    LoginService
	sessions SessionService
	control  ControlService
	health   http.Handler
	now      func() time.Time
}

func NewServer(config ServerConfig, logger *slog.Logger, login LoginService, sessions SessionService, control ControlService, health http.Handler) (*Server, error) {
	if config.SessionCookieName == "" || !config.SecureCookies {
		return nil, fmt.Errorf("secure session cookie configuration is required")
	}
	if logger == nil || login == nil || sessions == nil || control == nil || health == nil {
		return nil, fmt.Errorf("API dependencies are required")
	}
	return &Server{
		config:   config,
		logger:   logger,
		login:    login,
		sessions: sessions,
		control:  control,
		health:   health,
		now:      time.Now,
	}, nil
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/health", server.method(http.MethodGet, server.health))
	mux.Handle("/api/v1/auth/login", server.method(http.MethodPost, http.HandlerFunc(server.loginHandler)))
	mux.Handle("/api/v1/auth/logout", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.logoutHandler))))
	mux.Handle("/api/v1/session", server.method(http.MethodGet, server.requireSession(http.HandlerFunc(server.sessionHandler))))
	mux.Handle("/api/v1/control/health", server.method(http.MethodGet, server.requireSession(http.HandlerFunc(server.controlHealthHandler))))
	mux.Handle("/api/", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		WriteError(writer, request, NewError(http.StatusNotFound, CodeNotFound, "Endpoint not found.", nil))
	}))
	return SecurityHeaders(OperationIDs(server.logger, nil, Recover(server.logger, mux)))
}

func (server *Server) method(method string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != method {
			writer.Header().Set("Allow", method)
			WriteError(writer, request, NewError(http.StatusMethodNotAllowed, CodeMethodNotAllow, "Method not allowed.", nil))
			return
		}
		next.ServeHTTP(writer, request)
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (server *Server) loginHandler(writer http.ResponseWriter, request *http.Request) {
	var input loginRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid request body.", err))
		return
	}
	clientAddress, err := remoteAddress(request.RemoteAddr)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid client address.", err))
		return
	}
	tokens, err := server.login.Login(request.Context(), input.Username, input.Password, clientAddress, server.now().UTC())
	switch {
	case errors.Is(err, auth.ErrLoginThrottled):
		writer.Header().Set("Retry-After", "60")
		WriteError(writer, request, NewError(http.StatusTooManyRequests, CodeRateLimited, "Too many login attempts.", err))
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		WriteError(writer, request, NewError(http.StatusUnauthorized, CodeUnauthorized, "Invalid credentials.", err))
		return
	case err != nil:
		server.logger.ErrorContext(request.Context(), "login failed internally", "error_type", fmt.Sprintf("%T", err))
		WriteError(writer, request, NewError(http.StatusInternalServerError, CodeInternal, "An internal error occurred.", err))
		return
	}
	http.SetCookie(writer, auth.SessionCookie(server.config.SessionCookieName, tokens.SessionToken, tokens.ExpiresAt, server.config.SecureCookies))
	if err := WriteJSON(writer, http.StatusOK, loginResponse{CSRFToken: tokens.CSRFToken, ExpiresAt: tokens.ExpiresAt}); err != nil {
		server.logger.ErrorContext(request.Context(), "write login response failed", "error_type", fmt.Sprintf("%T", err))
	}
}

func (server *Server) logoutHandler(writer http.ResponseWriter, request *http.Request) {
	cookie, _ := request.Cookie(server.config.SessionCookieName)
	if cookie != nil {
		if err := server.sessions.Revoke(request.Context(), cookie.Value, server.now().UTC()); err != nil && !errors.Is(err, auth.ErrInvalidSession) {
			WriteError(writer, request, NewError(http.StatusInternalServerError, CodeInternal, "An internal error occurred.", err))
			return
		}
	}
	http.SetCookie(writer, auth.ExpiredSessionCookie(server.config.SessionCookieName, server.config.SecureCookies))
	writer.WriteHeader(http.StatusNoContent)
}

type sessionResponse struct {
	AdminID   string    `json:"admin_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (server *Server) sessionHandler(writer http.ResponseWriter, request *http.Request) {
	principal := principalFromContext(request.Context())
	_ = WriteJSON(writer, http.StatusOK, sessionResponse{AdminID: principal.Session.AdminID, ExpiresAt: principal.Session.ExpiresAt})
}

func (server *Server) controlHealthHandler(writer http.ResponseWriter, request *http.Request) {
	if err := server.control.Health(request.Context()); err != nil {
		server.logger.ErrorContext(request.Context(), "egressd health failed", "error_type", fmt.Sprintf("%T", err))
		WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "Privileged service unavailable.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

type principal struct {
	Session database.Session
}

type principalContextKey struct{}

func principalFromContext(ctx context.Context) principal {
	value, _ := ctx.Value(principalContextKey{}).(principal)
	return value
}

func (server *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(server.config.SessionCookieName)
		if err != nil || cookie.Value == "" {
			WriteError(writer, request, NewError(http.StatusUnauthorized, CodeUnauthorized, "Authentication required.", auth.ErrInvalidSession))
			return
		}
		session, err := server.sessions.Authenticate(request.Context(), cookie.Value, server.now().UTC())
		if err != nil {
			if errors.Is(err, auth.ErrInvalidSession) {
				WriteError(writer, request, NewError(http.StatusUnauthorized, CodeUnauthorized, "Authentication required.", err))
				return
			}
			WriteError(writer, request, NewError(http.StatusInternalServerError, CodeInternal, "An internal error occurred.", err))
			return
		}
		if requiresCSRF(request.Method) {
			if err := server.sessions.VerifyCSRF(session, request.Header.Get(CSRFHeader)); err != nil {
				WriteError(writer, request, NewError(http.StatusForbidden, CodeInvalidCSRF, "CSRF validation failed.", err))
				return
			}
		}
		ctx := context.WithValue(request.Context(), principalContextKey{}, principal{Session: session})
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func requiresCSRF(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func decodeJSON(request *http.Request, output any) error {
	contentType := request.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return fmt.Errorf("Content-Type must be application/json")
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, maximumJSONBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maximumJSONBytes {
		return fmt.Errorf("request body exceeds %d bytes", maximumJSONBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("request contains trailing JSON")
	}
	return nil
}

func remoteAddress(value string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse remote address: %w", err)
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse remote IP: %w", err)
	}
	return address.Unmap(), nil
}
