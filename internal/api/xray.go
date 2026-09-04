package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/logging"
	managedXray "github.com/egress-manager/egress-manager/internal/xray"
)

type storedXrayBindingDocument struct {
	Binding   domain.XrayBinding `json:"binding"`
	Revision  int64              `json:"revision"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

type updateXrayBindingRequest struct {
	Binding          domain.XrayBinding `json:"binding"`
	ExpectedRevision int64              `json:"expected_revision"`
}

func (server *Server) xrayDiscoveryHandler(writer http.ResponseWriter, request *http.Request) {
	report, err := server.control.DiscoverXray(request.Context())
	if err != nil {
		WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "Xray discovery is unavailable.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, report)
}

func (server *Server) xrayBindingsHandler(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		server.listXrayBindings(writer, request)
	case http.MethodPost:
		server.createXrayBinding(writer, request)
	case http.MethodPut:
		server.updateXrayBinding(writer, request)
	case http.MethodDelete:
		server.deleteXrayBinding(writer, request)
	default:
		writer.Header().Set("Allow", "GET, POST, PUT, DELETE")
		WriteError(writer, request, NewError(http.StatusMethodNotAllowed, CodeMethodNotAllow, "Method not allowed.", nil))
	}
}

func (server *Server) listXrayBindings(writer http.ResponseWriter, request *http.Request) {
	after, limit, err := pageParameters(request)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid Xray binding page request.", err))
		return
	}
	items, err := server.xray.ListXrayBindings(request.Context(), after, limit+1)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid Xray binding list request.", err))
		return
	}
	next := domain.ID("")
	if len(items) > limit {
		next = items[limit-1].Binding.ID
		items = items[:limit]
	}
	documents := make([]storedXrayBindingDocument, 0, len(items))
	for _, item := range items {
		documents = append(documents, xrayBindingDocument(item))
	}
	_ = WriteJSON(writer, http.StatusOK, map[string]any{"items": documents, "next_cursor": next})
}

func (server *Server) createXrayBinding(writer http.ResponseWriter, request *http.Request) {
	var input domain.XrayBinding
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid Xray binding.", err))
		return
	}
	stored, err := server.xray.CreateXrayBinding(request.Context(), input, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Xray binding or enabled inbound already exists.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid Xray binding.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusCreated, xrayBindingDocument(stored))
}

func (server *Server) updateXrayBinding(writer http.ResponseWriter, request *http.Request) {
	var input updateXrayBindingRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid Xray binding update.", err))
		return
	}
	stored, err := server.xray.UpdateXrayBinding(request.Context(), input.Binding, input.ExpectedRevision, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Xray binding changed or inbound is already active; refresh and retry.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid Xray binding update.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, xrayBindingDocument(stored))
}

func (server *Server) deleteXrayBinding(writer http.ResponseWriter, request *http.Request) {
	var input deleteForwardRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid Xray binding deletion.", err))
		return
	}
	if err := server.xray.DeleteXrayBinding(request.Context(), input.ID, input.ExpectedRevision); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, database.ErrConflict) {
			status = http.StatusConflict
		}
		WriteError(writer, request, NewError(status, CodeConflict, "Xray binding changed; refresh and retry.", err))
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) xrayPlanHandler(writer http.ResponseWriter, request *http.Request) {
	var input struct{}
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid Xray plan request.", err))
		return
	}
	plan, err := server.control.PlanXray(request.Context())
	if err != nil {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Xray plan rejected by safety checks.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, plan)
}

func (server *Server) xrayApplyHandler(writer http.ResponseWriter, request *http.Request) {
	var input managedXray.FragmentApplyRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid Xray apply request.", err))
		return
	}
	input.TransactionID = domain.ID("xray_" + logging.OperationID(request.Context()))
	if input.ExpectedForeignStateHash == "" || input.ExpectedFragmentStateHash == "" || input.ExpectedCandidateHash == "" {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "All reviewed Xray plan hashes are required.", nil))
		return
	}
	result, err := server.control.ApplyXray(request.Context(), input)
	if err != nil {
		WriteMutationError(writer, request, "Xray apply failed and rollback was attempted.", err)
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}

func xrayBindingDocument(item database.StoredXrayBinding) storedXrayBindingDocument {
	return storedXrayBindingDocument{Binding: item.Binding, Revision: item.Revision, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}
