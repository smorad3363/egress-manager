package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/logging"
	"github.com/egress-manager/egress-manager/internal/routeengine"
)

type storedRouteDocument struct {
	Route     domain.Route `json:"route"`
	Revision  int64        `json:"revision"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type updateRouteRequest struct {
	Route            domain.Route `json:"route"`
	ExpectedRevision int64        `json:"expected_revision"`
}

func (server *Server) routesHandler(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		server.listRoutes(writer, request)
	case http.MethodPost:
		server.createRoute(writer, request)
	case http.MethodPut:
		server.updateRoute(writer, request)
	case http.MethodDelete:
		server.deleteRoute(writer, request)
	default:
		writer.Header().Set("Allow", "GET, POST, PUT, DELETE")
		WriteError(writer, request, NewError(http.StatusMethodNotAllowed, CodeMethodNotAllow, "Method not allowed.", nil))
	}
}

func (server *Server) listRoutes(writer http.ResponseWriter, request *http.Request) {
	after, limit, err := pageParameters(request)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid route page request.", err))
		return
	}
	items, err := server.routes.ListRoutes(request.Context(), after, limit+1)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid route list request.", err))
		return
	}
	next := domain.ID("")
	if len(items) > limit {
		next = items[limit-1].Route.ID
		items = items[:limit]
	}
	documents := make([]storedRouteDocument, 0, len(items))
	for _, item := range items {
		documents = append(documents, routeDocument(item))
	}
	_ = WriteJSON(writer, http.StatusOK, map[string]any{"items": documents, "next_cursor": next})
}

func (server *Server) createRoute(writer http.ResponseWriter, request *http.Request) {
	var input domain.Route
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid egress route.", err))
		return
	}
	stored, err := server.routes.CreateRoute(request.Context(), input, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Egress route or enabled source already exists.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid egress route.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusCreated, routeDocument(stored))
}

func (server *Server) updateRoute(writer http.ResponseWriter, request *http.Request) {
	var input updateRouteRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid egress route update.", err))
		return
	}
	stored, err := server.routes.UpdateRoute(request.Context(), input.Route, input.ExpectedRevision, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Egress route changed; refresh and retry.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid egress route update.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, routeDocument(stored))
}

func (server *Server) deleteRoute(writer http.ResponseWriter, request *http.Request) {
	var input deleteForwardRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid egress route deletion.", err))
		return
	}
	if err := server.routes.DeleteRoute(request.Context(), input.ID, input.ExpectedRevision); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, database.ErrConflict) {
			status = http.StatusConflict
		}
		WriteError(writer, request, NewError(status, CodeConflict, "Egress route changed; refresh and retry.", err))
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) planRoutesHandler(writer http.ResponseWriter, request *http.Request) {
	var input struct{}
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid route plan request.", err))
		return
	}
	plan, err := server.control.PlanRoutes(request.Context())
	if err != nil {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Egress route plan rejected by safety checks.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, plan)
}

func (server *Server) applyRoutesHandler(writer http.ResponseWriter, request *http.Request) {
	var input routeengine.ApplyRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid route apply request.", err))
		return
	}
	input.TransactionID = domain.ID("route_" + logging.OperationID(request.Context()))
	if input.ExpectedSingBoxStateHash == "" || input.ExpectedRoutingStateHash == "" || input.ExpectedInterfaceStateHash == "" || input.ExpectedSingBoxCandidate == "" || input.ExpectedRoutingCandidate == "" || input.ExpectedNativeCandidate == "" || input.ExpectedCombinedCandidate == "" {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "All reviewed route plan hashes are required.", nil))
		return
	}
	result, err := server.control.ApplyRoutes(request.Context(), input)
	if err != nil {
		WriteMutationError(writer, request, "Egress route apply failed and rollback was attempted.", err)
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}

func routeDocument(item database.StoredRoute) storedRouteDocument {
	return storedRouteDocument{Route: item.Route, Revision: item.Revision, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}
