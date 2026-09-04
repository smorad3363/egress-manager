package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	managedHAProxy "github.com/egress-manager/egress-manager/internal/haproxy"
	"github.com/egress-manager/egress-manager/internal/logging"
)

const maximumHAProxyJSONBytes = 128 << 10

type storedHAProxyBackendDocument struct {
	Backend   domain.HAProxyBackend `json:"backend"`
	Revision  int64                 `json:"revision"`
	CreatedAt time.Time             `json:"created_at"`
	UpdatedAt time.Time             `json:"updated_at"`
}

type storedHAProxyFrontendDocument struct {
	Frontend  domain.HAProxyFrontend `json:"frontend"`
	Revision  int64                  `json:"revision"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

func (server *Server) haproxyBackendsHandler(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		server.listHAProxyBackends(writer, request)
	case http.MethodPost:
		server.createHAProxyBackend(writer, request)
	case http.MethodPut:
		server.updateHAProxyBackend(writer, request)
	case http.MethodDelete:
		server.deleteHAProxyBackend(writer, request)
	default:
		writer.Header().Set("Allow", "GET, POST, PUT, DELETE")
		WriteError(writer, request, NewError(http.StatusMethodNotAllowed, CodeMethodNotAllow, "Method not allowed.", nil))
	}
}

func (server *Server) haproxyFrontendsHandler(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		server.listHAProxyFrontends(writer, request)
	case http.MethodPost:
		server.createHAProxyFrontend(writer, request)
	case http.MethodPut:
		server.updateHAProxyFrontend(writer, request)
	case http.MethodDelete:
		server.deleteHAProxyFrontend(writer, request)
	default:
		writer.Header().Set("Allow", "GET, POST, PUT, DELETE")
		WriteError(writer, request, NewError(http.StatusMethodNotAllowed, CodeMethodNotAllow, "Method not allowed.", nil))
	}
}

func pageParameters(request *http.Request) (domain.ID, int, error) {
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			return "", 0, errors.New("invalid page limit")
		}
		limit = parsed
	}
	return domain.ID(request.URL.Query().Get("after")), limit, nil
}

func (server *Server) listHAProxyBackends(writer http.ResponseWriter, request *http.Request) {
	after, limit, err := pageParameters(request)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid page request.", err))
		return
	}
	items, err := server.haproxy.ListHAProxyBackends(request.Context(), after, limit+1)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid backend list request.", err))
		return
	}
	documents := make([]storedHAProxyBackendDocument, 0, min(len(items), limit))
	next := domain.ID("")
	if len(items) > limit {
		next = items[limit-1].Backend.ID
		items = items[:limit]
	}
	for _, item := range items {
		documents = append(documents, backendDocument(item))
	}
	_ = WriteJSON(writer, http.StatusOK, map[string]any{"items": documents, "next_cursor": next})
}

func (server *Server) listHAProxyFrontends(writer http.ResponseWriter, request *http.Request) {
	after, limit, err := pageParameters(request)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid page request.", err))
		return
	}
	items, err := server.haproxy.ListHAProxyFrontends(request.Context(), after, limit+1)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid frontend list request.", err))
		return
	}
	documents := make([]storedHAProxyFrontendDocument, 0, min(len(items), limit))
	next := domain.ID("")
	if len(items) > limit {
		next = items[limit-1].Frontend.ID
		items = items[:limit]
	}
	for _, item := range items {
		documents = append(documents, frontendDocument(item))
	}
	_ = WriteJSON(writer, http.StatusOK, map[string]any{"items": documents, "next_cursor": next})
}

func (server *Server) createHAProxyBackend(writer http.ResponseWriter, request *http.Request) {
	var backend domain.HAProxyBackend
	if err := decodeJSON(request, &backend); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy backend.", err))
		return
	}
	stored, err := server.haproxy.CreateHAProxyBackend(request.Context(), backend, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "HAProxy backend already exists.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy backend.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusCreated, backendDocument(stored))
}

type updateHAProxyBackendRequest struct {
	Backend          domain.HAProxyBackend `json:"backend"`
	ExpectedRevision int64                 `json:"expected_revision"`
}

func (server *Server) updateHAProxyBackend(writer http.ResponseWriter, request *http.Request) {
	var input updateHAProxyBackendRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy backend update.", err))
		return
	}
	stored, err := server.haproxy.UpdateHAProxyBackend(request.Context(), input.Backend, input.ExpectedRevision, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "HAProxy backend changed; refresh and retry.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy backend update.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, backendDocument(stored))
}

func (server *Server) deleteHAProxyBackend(writer http.ResponseWriter, request *http.Request) {
	var input deleteForwardRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy backend deletion.", err))
		return
	}
	if err := server.haproxy.DeleteHAProxyBackend(request.Context(), input.ID, input.ExpectedRevision); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, database.ErrConflict) {
			status = http.StatusConflict
		}
		WriteError(writer, request, NewError(status, CodeConflict, "HAProxy backend is referenced or changed; refresh and retry.", err))
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) createHAProxyFrontend(writer http.ResponseWriter, request *http.Request) {
	var frontend domain.HAProxyFrontend
	if err := decodeJSON(request, &frontend); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy frontend.", err))
		return
	}
	stored, err := server.haproxy.CreateHAProxyFrontend(request.Context(), frontend, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "HAProxy frontend or backend reference conflicts.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy frontend.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusCreated, frontendDocument(stored))
}

type updateHAProxyFrontendRequest struct {
	Frontend         domain.HAProxyFrontend `json:"frontend"`
	ExpectedRevision int64                  `json:"expected_revision"`
}

func (server *Server) updateHAProxyFrontend(writer http.ResponseWriter, request *http.Request) {
	var input updateHAProxyFrontendRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy frontend update.", err))
		return
	}
	stored, err := server.haproxy.UpdateHAProxyFrontend(request.Context(), input.Frontend, input.ExpectedRevision, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "HAProxy frontend changed or references a missing backend.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy frontend update.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, frontendDocument(stored))
}

func (server *Server) deleteHAProxyFrontend(writer http.ResponseWriter, request *http.Request) {
	var input deleteForwardRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy frontend deletion.", err))
		return
	}
	if err := server.haproxy.DeleteHAProxyFrontend(request.Context(), input.ID, input.ExpectedRevision); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, database.ErrConflict) {
			status = http.StatusConflict
		}
		WriteError(writer, request, NewError(status, CodeConflict, "HAProxy frontend changed; refresh and retry.", err))
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) haproxyPlanHandler(writer http.ResponseWriter, request *http.Request) {
	var input managedHAProxy.PlanRequest
	if err := decodeJSONLimit(request, &input, maximumHAProxyJSONBytes); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy plan request.", err))
		return
	}
	plan, err := server.control.PlanHAProxy(request.Context(), input)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "HAProxy plan rejected by safety checks.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, plan)
}

func (server *Server) haproxyApplyHandler(writer http.ResponseWriter, request *http.Request) {
	var input struct{}
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid HAProxy apply request.", err))
		return
	}
	frontends, backends, err := server.loadHAProxyDesired(request.Context())
	if err != nil {
		WriteError(writer, request, NewError(http.StatusInternalServerError, CodeInternal, "Unable to read HAProxy desired state.", err))
		return
	}
	requested, err := json.Marshal(managedHAProxy.PlanRequest{Frontends: frontends, Backends: backends})
	if err != nil {
		WriteError(writer, request, NewError(http.StatusInternalServerError, CodeInternal, "Unable to encode HAProxy desired state.", err))
		return
	}
	transactionID := domain.ID("haproxy_" + logging.OperationID(request.Context()))
	result, err := server.control.ApplyHAProxy(request.Context(), managedHAProxy.ApplyRequest{TransactionID: transactionID, RequestedChange: string(requested), Frontends: frontends, Backends: backends})
	if err != nil {
		WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "HAProxy apply failed and rollback was attempted.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}

func (server *Server) haproxyStatsHandler(writer http.ResponseWriter, request *http.Request) {
	result, err := server.control.HAProxyStats(request.Context())
	if err != nil {
		WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "HAProxy Runtime API unavailable.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}

func (server *Server) loadHAProxyDesired(ctx context.Context) ([]domain.HAProxyFrontend, []domain.HAProxyBackend, error) {
	storedFrontends, err := server.haproxy.ListHAProxyFrontends(ctx, "", managedHAProxy.MaximumFrontends+1)
	if err != nil {
		return nil, nil, err
	}
	storedBackends, err := server.haproxy.ListHAProxyBackends(ctx, "", managedHAProxy.MaximumBackends+1)
	if err != nil {
		return nil, nil, err
	}
	if len(storedFrontends) > managedHAProxy.MaximumFrontends || len(storedBackends) > managedHAProxy.MaximumBackends {
		return nil, nil, errors.New("HAProxy desired state exceeds atomic apply limits")
	}
	frontends := make([]domain.HAProxyFrontend, 0, len(storedFrontends))
	for _, item := range storedFrontends {
		frontends = append(frontends, item.Frontend)
	}
	backends := make([]domain.HAProxyBackend, 0, len(storedBackends))
	for _, item := range storedBackends {
		backends = append(backends, item.Backend)
	}
	return frontends, backends, nil
}

func backendDocument(item database.StoredHAProxyBackend) storedHAProxyBackendDocument {
	return storedHAProxyBackendDocument{Backend: item.Backend, Revision: item.Revision, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}
func frontendDocument(item database.StoredHAProxyFrontend) storedHAProxyFrontendDocument {
	return storedHAProxyFrontendDocument{Frontend: item.Frontend, Revision: item.Revision, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}
