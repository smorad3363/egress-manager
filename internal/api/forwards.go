package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/logging"
	"github.com/egress-manager/egress-manager/internal/nat"
)

type storedForwardDocument struct {
	Forward   domain.PortForward `json:"forward"`
	Revision  int64              `json:"revision"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

type forwardListDocument struct {
	Items      []storedForwardDocument `json:"items"`
	NextCursor domain.ID               `json:"next_cursor,omitempty"`
}

func (server *Server) portForwardsHandler(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		server.listPortForwardsHandler(writer, request)
	case http.MethodPost:
		server.createPortForwardHandler(writer, request)
	case http.MethodPut:
		server.updatePortForwardHandler(writer, request)
	case http.MethodDelete:
		server.deletePortForwardHandler(writer, request)
	default:
		writer.Header().Set("Allow", "GET, POST, PUT, DELETE")
		WriteError(writer, request, NewError(http.StatusMethodNotAllowed, CodeMethodNotAllow, "Method not allowed.", nil))
	}
}

func (server *Server) listPortForwardsHandler(writer http.ResponseWriter, request *http.Request) {
	limit := 100
	if rawLimit := request.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > 500 {
			WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid page limit.", err))
			return
		}
		limit = parsed
	}
	after := domain.ID(request.URL.Query().Get("after"))
	stored, err := server.forwards.ListPortForwards(request.Context(), after, limit+1)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid list request.", err))
		return
	}
	document := forwardListDocument{Items: []storedForwardDocument{}}
	if len(stored) > limit {
		document.NextCursor = stored[limit-1].Forward.ID
		stored = stored[:limit]
	}
	for _, item := range stored {
		document.Items = append(document.Items, forwardDocument(item))
	}
	_ = WriteJSON(writer, http.StatusOK, document)
}

func (server *Server) createPortForwardHandler(writer http.ResponseWriter, request *http.Request) {
	var forward domain.PortForward
	if err := decodeJSON(request, &forward); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid port forward.", err))
		return
	}
	stored, err := server.forwards.CreatePortForward(request.Context(), forward, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Port forward already exists.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid port forward.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusCreated, forwardDocument(stored))
}

type updateForwardRequest struct {
	Forward          domain.PortForward `json:"forward"`
	ExpectedRevision int64              `json:"expected_revision"`
}

func (server *Server) updatePortForwardHandler(writer http.ResponseWriter, request *http.Request) {
	var input updateForwardRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid port forward update.", err))
		return
	}
	stored, err := server.forwards.UpdatePortForward(request.Context(), input.Forward, input.ExpectedRevision, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Port forward changed; refresh and retry.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid port forward update.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, forwardDocument(stored))
}

type deleteForwardRequest struct {
	ID               domain.ID `json:"id"`
	ExpectedRevision int64     `json:"expected_revision"`
}

func (server *Server) deletePortForwardHandler(writer http.ResponseWriter, request *http.Request) {
	var input deleteForwardRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid port forward deletion.", err))
		return
	}
	if err := server.forwards.DeletePortForward(request.Context(), input.ID, input.ExpectedRevision); err != nil {
		if errors.Is(err, database.ErrConflict) {
			WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Port forward changed; refresh and retry.", err))
			return
		}
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid port forward deletion.", err))
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) planPortForwardsHandler(writer http.ResponseWriter, request *http.Request) {
	var input nat.PlanRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid NAT plan request.", err))
		return
	}
	plan, err := server.control.PlanNAT(request.Context(), input)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "NAT plan rejected by host safety checks.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, plan)
}

type applyForwardsRequest struct {
	Family nat.AddressFamily `json:"family"`
}

func (server *Server) applyPortForwardsHandler(writer http.ResponseWriter, request *http.Request) {
	var input applyForwardsRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid NAT apply request.", err))
		return
	}
	stored, err := server.forwards.ListPortForwards(request.Context(), "", 500)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusInternalServerError, CodeInternal, "Unable to read desired port forwards.", err))
		return
	}
	forwards := make([]domain.PortForward, 0, len(stored))
	for _, item := range stored {
		forwards = append(forwards, item.Forward)
	}
	if len(forwards) > nat.MaximumForwards {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Too many port forwards are configured for one atomic apply.", nil))
		return
	}
	requested, err := json.Marshal(forwards)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusInternalServerError, CodeInternal, "Unable to encode desired port forwards.", err))
		return
	}
	transactionID := domain.ID("nat_" + logging.OperationID(request.Context()))
	result, err := server.control.ApplyNAT(request.Context(), nat.ApplyRequest{
		TransactionID: transactionID, RequestedChange: string(requested), Family: input.Family, Forwards: forwards,
	})
	if err != nil {
		WriteMutationError(writer, request, "NAT apply failed and rollback was attempted.", err)
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}

func (server *Server) portForwardCountersHandler(writer http.ResponseWriter, request *http.Request) {
	family := nat.AddressFamily(request.URL.Query().Get("family"))
	result, err := server.control.NATCounters(request.Context(), nat.CounterRequest{Family: family})
	if err != nil {
		WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "NAT counters unavailable.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}

func forwardDocument(stored database.StoredPortForward) storedForwardDocument {
	return storedForwardDocument{Forward: stored.Forward, Revision: stored.Revision, CreatedAt: stored.CreatedAt, UpdatedAt: stored.UpdatedAt}
}
