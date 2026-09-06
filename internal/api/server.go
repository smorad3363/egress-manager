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
	"github.com/egress-manager/egress-manager/internal/domain"
	managedHAProxy "github.com/egress-manager/egress-manager/internal/haproxy"
	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/nat"
	"github.com/egress-manager/egress-manager/internal/routeengine"
	managedSingBox "github.com/egress-manager/egress-manager/internal/singbox"
	managedXray "github.com/egress-manager/egress-manager/internal/xray"
	"github.com/egress-manager/egress-manager/internal/xrayrelay"
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
	Inventory(context.Context) (inventory.Inventory, error)
	PlanNAT(context.Context, nat.PlanRequest) (nat.Plan, error)
	ApplyNAT(context.Context, nat.ApplyRequest) (nat.ApplyResponse, error)
	NATCounters(context.Context, nat.CounterRequest) (nat.CounterSnapshot, error)
	PlanHAProxy(context.Context, managedHAProxy.PlanRequest) (managedHAProxy.Plan, error)
	ApplyHAProxy(context.Context, managedHAProxy.ApplyRequest) (managedHAProxy.ApplyResponse, error)
	HAProxyStats(context.Context) (managedHAProxy.RuntimeSnapshot, error)
	ImportSingBox(context.Context, managedSingBox.ImportRequest) (managedSingBox.ImportResponse, error)
	TestSingBox(context.Context, managedSingBox.TestRequest) (managedSingBox.TestResponse, error)
	PlanSingBox(context.Context) (managedSingBox.Plan, error)
	ApplySingBox(context.Context, managedSingBox.ApplyRequest) (managedSingBox.ApplyResponse, error)
	ImportXrayRelayOutbound(context.Context, xrayrelay.ImportRequest) (xrayrelay.ImportResponse, error)
	TestXrayRelayOutbound(context.Context, xrayrelay.TestRequest) (xrayrelay.TestResponse, error)
	PlanRelays(context.Context) (xrayrelay.Plan, error)
	ApplyRelays(context.Context, xrayrelay.ApplyRequest) (xrayrelay.ApplyResponse, error)
	PlanRoutes(context.Context) (routeengine.Review, error)
	ApplyRoutes(context.Context, routeengine.ApplyRequest) (routeengine.ApplyResponse, error)
	DiscoverXray(context.Context) (managedXray.Report, error)
	PlanXray(context.Context) (managedXray.FragmentReview, error)
	ApplyXray(context.Context, managedXray.FragmentApplyRequest) (managedXray.FragmentApplyResponse, error)
	ImportInterfaceOutbound(context.Context, managedInterface.ImportRequest) (managedInterface.ImportResponse, error)
	TestInterfaceOutbound(context.Context, managedInterface.TestRequest) (managedInterface.TestResponse, error)
	PlanInterfaceOutbounds(context.Context) (managedInterface.Review, error)
	ApplyInterfaceOutbounds(context.Context, managedInterface.ApplyRequest) (managedInterface.ApplyResponse, error)
}

type ForwardRepository interface {
	CreatePortForward(context.Context, domain.PortForward, time.Time) (database.StoredPortForward, error)
	UpdatePortForward(context.Context, domain.PortForward, int64, time.Time) (database.StoredPortForward, error)
	DeletePortForward(context.Context, domain.ID, int64) error
	ListPortForwards(context.Context, domain.ID, int) ([]database.StoredPortForward, error)
}

type HAProxyRepository interface {
	CreateHAProxyBackend(context.Context, domain.HAProxyBackend, time.Time) (database.StoredHAProxyBackend, error)
	UpdateHAProxyBackend(context.Context, domain.HAProxyBackend, int64, time.Time) (database.StoredHAProxyBackend, error)
	DeleteHAProxyBackend(context.Context, domain.ID, int64) error
	ListHAProxyBackends(context.Context, domain.ID, int) ([]database.StoredHAProxyBackend, error)
	CreateHAProxyFrontend(context.Context, domain.HAProxyFrontend, time.Time) (database.StoredHAProxyFrontend, error)
	UpdateHAProxyFrontend(context.Context, domain.HAProxyFrontend, int64, time.Time) (database.StoredHAProxyFrontend, error)
	DeleteHAProxyFrontend(context.Context, domain.ID, int64) error
	ListHAProxyFrontends(context.Context, domain.ID, int) ([]database.StoredHAProxyFrontend, error)
}

type OutboundRepository interface {
	Outbound(context.Context, domain.ID) (database.StoredOutbound, error)
	UpdateOutbound(context.Context, domain.Outbound, int64, []byte, time.Time) (database.StoredOutbound, error)
	DeleteOutbound(context.Context, domain.ID, int64) error
	CloneOutbound(context.Context, domain.ID, int64, domain.Outbound, time.Time) (database.StoredOutbound, error)
	ListOutbounds(context.Context, domain.ID, int) ([]database.StoredOutbound, error)
}

type RouteRepository interface {
	CreateRoute(context.Context, domain.Route, time.Time) (database.StoredRoute, error)
	UpdateRoute(context.Context, domain.Route, int64, time.Time) (database.StoredRoute, error)
	DeleteRoute(context.Context, domain.ID, int64) error
	ListRoutes(context.Context, domain.ID, int) ([]database.StoredRoute, error)
}

type RelayRepository interface {
	CreateRelay(context.Context, domain.Relay, time.Time) (database.StoredRelay, error)
	UpdateRelay(context.Context, domain.Relay, int64, time.Time) (database.StoredRelay, error)
	DeleteRelay(context.Context, domain.ID, int64) error
	ListRelays(context.Context, domain.ID, int) ([]database.StoredRelay, error)
}

type XrayBindingRepository interface {
	CreateXrayBinding(context.Context, domain.XrayBinding, time.Time) (database.StoredXrayBinding, error)
	UpdateXrayBinding(context.Context, domain.XrayBinding, int64, time.Time) (database.StoredXrayBinding, error)
	DeleteXrayBinding(context.Context, domain.ID, int64) error
	ListXrayBindings(context.Context, domain.ID, int) ([]database.StoredXrayBinding, error)
}

type ServerConfig struct {
	SessionCookieName string
	SecureCookies     bool
}

type Server struct {
	config    ServerConfig
	logger    *slog.Logger
	login     LoginService
	sessions  SessionService
	control   ControlService
	forwards  ForwardRepository
	haproxy   HAProxyRepository
	outbounds OutboundRepository
	routes    RouteRepository
	relays    RelayRepository
	xray      XrayBindingRepository
	health    http.Handler
	now       func() time.Time
}

func NewServer(config ServerConfig, logger *slog.Logger, login LoginService, sessions SessionService, control ControlService, forwards ForwardRepository, haproxy HAProxyRepository, outbounds OutboundRepository, routes RouteRepository, relays RelayRepository, xray XrayBindingRepository, health http.Handler) (*Server, error) {
	if config.SessionCookieName == "" || !config.SecureCookies {
		return nil, fmt.Errorf("secure session cookie configuration is required")
	}
	if logger == nil || login == nil || sessions == nil || control == nil || forwards == nil || haproxy == nil || outbounds == nil || routes == nil || relays == nil || xray == nil || health == nil {
		return nil, fmt.Errorf("API dependencies are required")
	}
	return &Server{
		config:    config,
		logger:    logger,
		login:     login,
		sessions:  sessions,
		control:   control,
		forwards:  forwards,
		haproxy:   haproxy,
		outbounds: outbounds,
		routes:    routes,
		relays:    relays,
		xray:      xray,
		health:    health,
		now:       time.Now,
	}, nil
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/health", server.methods([]string{http.MethodGet, http.MethodHead}, server.health))
	mux.Handle("/api/v1/auth/login", server.method(http.MethodPost, http.HandlerFunc(server.loginHandler)))
	mux.Handle("/api/v1/auth/logout", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.logoutHandler))))
	mux.Handle("/api/v1/session", server.method(http.MethodGet, server.requireSession(http.HandlerFunc(server.sessionHandler))))
	mux.Handle("/api/v1/control/health", server.method(http.MethodGet, server.requireSession(http.HandlerFunc(server.controlHealthHandler))))
	mux.Handle("/api/v1/network/inventory", server.method(http.MethodGet, server.requireSession(http.HandlerFunc(server.networkInventoryHandler))))
	mux.Handle("/api/v1/port-forwards/plan", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.planPortForwardsHandler))))
	mux.Handle("/api/v1/port-forwards/apply", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.applyPortForwardsHandler))))
	mux.Handle("/api/v1/port-forwards/counters", server.method(http.MethodGet, server.requireSession(http.HandlerFunc(server.portForwardCountersHandler))))
	mux.Handle("/api/v1/port-forwards", server.requireSession(http.HandlerFunc(server.portForwardsHandler)))
	mux.Handle("/api/v1/haproxy/backends", server.requireSession(http.HandlerFunc(server.haproxyBackendsHandler)))
	mux.Handle("/api/v1/haproxy/frontends", server.requireSession(http.HandlerFunc(server.haproxyFrontendsHandler)))
	mux.Handle("/api/v1/haproxy/plan", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.haproxyPlanHandler))))
	mux.Handle("/api/v1/haproxy/apply", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.haproxyApplyHandler))))
	mux.Handle("/api/v1/haproxy/stats", server.method(http.MethodGet, server.requireSession(http.HandlerFunc(server.haproxyStatsHandler))))
	mux.Handle("/api/v1/outbounds/import", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.importOutboundsHandler))))
	mux.Handle("/api/v1/outbounds/test", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.testOutboundsHandler))))
	mux.Handle("/api/v1/outbounds/clone", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.cloneOutboundHandler))))
	mux.Handle("/api/v1/outbounds/plan", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.planOutboundsHandler))))
	mux.Handle("/api/v1/outbounds/apply", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.applyOutboundsHandler))))
	mux.Handle("/api/v1/outbounds", server.requireSession(http.HandlerFunc(server.outboundsHandler)))
	mux.Handle("/api/v1/routes/plan", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.planRoutesHandler))))
	mux.Handle("/api/v1/routes/apply", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.applyRoutesHandler))))
	mux.Handle("/api/v1/routes", server.requireSession(http.HandlerFunc(server.routesHandler)))
	mux.Handle("/api/v1/relays/plan", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.planRelaysHandler))))
	mux.Handle("/api/v1/relays/apply", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.applyRelaysHandler))))
	mux.Handle("/api/v1/relays", server.requireSession(http.HandlerFunc(server.relaysHandler)))
	mux.Handle("/api/v1/xray/discovery", server.method(http.MethodGet, server.requireSession(http.HandlerFunc(server.xrayDiscoveryHandler))))
	mux.Handle("/api/v1/xray/bindings", server.requireSession(http.HandlerFunc(server.xrayBindingsHandler)))
	mux.Handle("/api/v1/xray/plan", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.xrayPlanHandler))))
	mux.Handle("/api/v1/xray/apply", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.xrayApplyHandler))))
	mux.Handle("/api/v1/interface-outbounds/import", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.interfaceOutboundImportHandler))))
	mux.Handle("/api/v1/interface-outbounds/test", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.interfaceOutboundTestHandler))))
	mux.Handle("/api/v1/interface-outbounds/plan", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.interfaceOutboundsPlanHandler))))
	mux.Handle("/api/v1/interface-outbounds/apply", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.interfaceOutboundsApplyHandler))))
	mux.Handle("/api/", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		WriteError(writer, request, NewError(http.StatusNotFound, CodeNotFound, "Endpoint not found.", nil))
	}))
	return SecurityHeaders(OperationIDs(server.logger, nil, Recover(server.logger, mux)))
}

func (server *Server) method(method string, next http.Handler) http.Handler {
	return server.methods([]string{method}, next)
}

func (server *Server) methods(methods []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		allowed := false
		for _, method := range methods {
			if request.Method == method {
				allowed = true
				break
			}
		}
		if !allowed {
			writer.Header().Set("Allow", strings.Join(methods, ", "))
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

func (server *Server) networkInventoryHandler(writer http.ResponseWriter, request *http.Request) {
	result, err := server.control.Inventory(request.Context())
	if err != nil {
		server.logger.ErrorContext(request.Context(), "network inventory failed", "error_type", fmt.Sprintf("%T", err))
		WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "Network inventory unavailable.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
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
	return decodeJSONLimit(request, output, maximumJSONBytes)
}

func decodeJSONLimit(request *http.Request, output any, limit int64) error {
	contentType := request.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return fmt.Errorf("Content-Type must be application/json")
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("request body exceeds %d bytes", limit)
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
