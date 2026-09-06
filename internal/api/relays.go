package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/logging"
	"github.com/egress-manager/egress-manager/internal/xrayrelay"
)

type storedRelayDocument struct {
	Relay     domain.Relay `json:"relay"`
	Revision  int64        `json:"revision"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type updateRelayRequest struct {
	Relay            domain.Relay `json:"relay"`
	ExpectedRevision int64        `json:"expected_revision"`
}

func (server *Server) relaysHandler(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		server.listRelays(writer, request)
	case http.MethodPost:
		server.createRelay(writer, request)
	case http.MethodPut:
		server.updateRelay(writer, request)
	case http.MethodDelete:
		server.deleteRelay(writer, request)
	default:
		writer.Header().Set("Allow", "GET, POST, PUT, DELETE")
		WriteError(writer, request, NewError(http.StatusMethodNotAllowed, CodeMethodNotAllow, "Method not allowed.", nil))
	}
}

func (server *Server) listRelays(writer http.ResponseWriter, request *http.Request) {
	after, limit, err := pageParameters(request)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid relay page request.", err))
		return
	}
	items, err := server.relays.ListRelays(request.Context(), after, limit+1)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid relay list request.", err))
		return
	}
	next := domain.ID("")
	if len(items) > limit {
		next = items[limit-1].Relay.ID
		items = items[:limit]
	}
	documents := make([]storedRelayDocument, 0, len(items))
	for _, item := range items {
		documents = append(documents, relayDocument(item))
	}
	_ = WriteJSON(writer, http.StatusOK, map[string]any{"items": documents, "next_cursor": next})
}

func (server *Server) createRelay(writer http.ResponseWriter, request *http.Request) {
	var input domain.Relay
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid listener relay.", err))
		return
	}
	stored, err := server.relays.CreateRelay(request.Context(), input, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Relay already exists.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid listener relay.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusCreated, relayDocument(stored))
}

func (server *Server) updateRelay(writer http.ResponseWriter, request *http.Request) {
	var input updateRelayRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid listener relay update.", err))
		return
	}
	stored, err := server.relays.UpdateRelay(request.Context(), input.Relay, input.ExpectedRevision, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Relay changed; refresh and retry.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid listener relay update.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, relayDocument(stored))
}

func (server *Server) deleteRelay(writer http.ResponseWriter, request *http.Request) {
	var input deleteForwardRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid relay deletion.", err))
		return
	}
	if err := server.relays.DeleteRelay(request.Context(), input.ID, input.ExpectedRevision); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, database.ErrConflict) {
			status = http.StatusConflict
		}
		WriteError(writer, request, NewError(status, CodeConflict, "Relay changed; refresh and retry.", err))
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) planRelaysHandler(writer http.ResponseWriter, request *http.Request) {
	var input struct{}
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid relay plan request.", err))
		return
	}
	plan, err := server.control.PlanRelays(request.Context())
	if err != nil {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Relay plan rejected by safety checks.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, plan)
}

func (server *Server) applyRelaysHandler(writer http.ResponseWriter, request *http.Request) {
	var input xrayrelay.ApplyRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid relay apply request.", err))
		return
	}
	if input.ExpectedStateHash == "" || input.ExpectedCandidateHash == "" {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Reviewed relay plan hashes are required.", nil))
		return
	}
	input.TransactionID = domain.ID("relay_" + logging.OperationID(request.Context()))
	result, err := server.control.ApplyRelays(request.Context(), input)
	if err != nil {
		WriteMutationError(writer, request, "Relay apply failed and rollback was attempted.", err)
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}

func relayDocument(item database.StoredRelay) storedRelayDocument {
	return storedRelayDocument{Relay: item.Relay, Revision: item.Revision, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}
