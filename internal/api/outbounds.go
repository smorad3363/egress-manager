package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/egress-manager/egress-manager/internal/database"
	"github.com/egress-manager/egress-manager/internal/domain"
	"github.com/egress-manager/egress-manager/internal/logging"
	managedSingBox "github.com/egress-manager/egress-manager/internal/singbox"
	"github.com/egress-manager/egress-manager/internal/xrayrelay"
)

const maximumOutboundJSONBytes = managedSingBox.MaximumImportBytes + 1024

type storedOutboundDocument struct {
	Outbound  domain.Outbound `json:"outbound"`
	Revision  int64           `json:"revision"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type updateOutboundRequest struct {
	Outbound         domain.Outbound `json:"outbound"`
	ExpectedRevision int64           `json:"expected_revision"`
}

type cloneOutboundRequest struct {
	SourceID         domain.ID `json:"source_id"`
	ExpectedRevision int64     `json:"expected_revision"`
	ID               domain.ID `json:"id"`
	Name             string    `json:"name"`
}

func (server *Server) outboundsHandler(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		server.listOutbounds(writer, request)
	case http.MethodPut:
		server.updateOutbound(writer, request)
	case http.MethodDelete:
		server.deleteOutbound(writer, request)
	default:
		writer.Header().Set("Allow", "GET, PUT, DELETE")
		WriteError(writer, request, NewError(http.StatusMethodNotAllowed, CodeMethodNotAllow, "Method not allowed.", nil))
	}
}

func (server *Server) listOutbounds(writer http.ResponseWriter, request *http.Request) {
	after, limit, err := pageParameters(request)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid page request.", err))
		return
	}
	items, err := server.outbounds.ListOutbounds(request.Context(), after, limit+1)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid outbound list request.", err))
		return
	}
	next := domain.ID("")
	if len(items) > limit {
		next = items[limit-1].Outbound.ID
		items = items[:limit]
	}
	documents := make([]storedOutboundDocument, 0, len(items))
	for _, item := range items {
		documents = append(documents, outboundDocument(item))
	}
	_ = WriteJSON(writer, http.StatusOK, map[string]any{"items": documents, "next_cursor": next})
}

func (server *Server) updateOutbound(writer http.ResponseWriter, request *http.Request) {
	var input updateOutboundRequest
	if err := decodeJSONLimit(request, &input, maximumOutboundJSONBytes); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid outbound update.", err))
		return
	}
	stored, err := server.outbounds.UpdateOutbound(request.Context(), input.Outbound, input.ExpectedRevision, nil, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Outbound changed; refresh and retry.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid outbound update.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, outboundDocument(stored))
}

func (server *Server) deleteOutbound(writer http.ResponseWriter, request *http.Request) {
	var input deleteForwardRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid outbound deletion.", err))
		return
	}
	if err := server.outbounds.DeleteOutbound(request.Context(), input.ID, input.ExpectedRevision); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, database.ErrConflict) {
			status = http.StatusConflict
		}
		WriteError(writer, request, NewError(status, CodeConflict, "Outbound changed; refresh and retry.", err))
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) importOutboundsHandler(writer http.ResponseWriter, request *http.Request) {
	var input managedSingBox.ImportRequest
	if err := decodeJSONLimit(request, &input, maximumOutboundJSONBytes); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid outbound import.", err))
		return
	}
	if xrayrelay.IsCompatibleImport(input.Input) {
		result, err := server.control.ImportXrayRelayOutbound(request.Context(), xrayrelay.ImportRequest{Input: input.Input})
		if err != nil {
			WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Outbound import rejected.", err))
			return
		}
		_ = WriteJSON(writer, http.StatusCreated, result)
		return
	}
	result, err := server.control.ImportSingBox(request.Context(), input)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Outbound import rejected.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusCreated, result)
}

func (server *Server) testOutboundsHandler(writer http.ResponseWriter, request *http.Request) {
	var input managedSingBox.TestRequest
	if err := decodeJSONLimit(request, &input, maximumOutboundJSONBytes); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid outbound test.", err))
		return
	}
	if (input.ID == "") == (input.Input == "") || input.ID != "" && input.ExpectedRevision < 1 {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Provide exactly one import input or stored outbound with its revision.", nil))
		return
	}
	useXray := input.Input != "" && xrayrelay.IsCompatibleImport(input.Input)
	if input.ID != "" {
		stored, lookupErr := server.outbounds.Outbound(request.Context(), input.ID)
		if lookupErr != nil {
			WriteError(writer, request, NewError(http.StatusNotFound, CodeNotFound, "Outbound not found.", lookupErr))
			return
		}
		useXray = stored.Outbound.Adapter == domain.OutboundAdapterXray
	}
	if useXray {
		result, err := server.control.TestXrayRelayOutbound(request.Context(), xrayrelay.TestRequest{ID: input.ID, ExpectedRevision: input.ExpectedRevision, Input: input.Input})
		if err != nil {
			WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "Outbound test failed.", err))
			return
		}
		_ = WriteJSON(writer, http.StatusOK, result)
		return
	}
	result, err := server.control.TestSingBox(request.Context(), input)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "Outbound test failed.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}

func (server *Server) cloneOutboundHandler(writer http.ResponseWriter, request *http.Request) {
	var input cloneOutboundRequest
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid outbound clone.", err))
		return
	}
	source, err := server.outbounds.Outbound(request.Context(), input.SourceID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, database.ErrNotFound) {
			status = http.StatusNotFound
		}
		WriteError(writer, request, NewError(status, CodeNotFound, "Source outbound not found.", err))
		return
	}
	clone := source.Outbound
	clone.ID = input.ID
	clone.Name = input.Name
	clone.Enabled = false
	clone.Health = domain.UnknownOutboundHealth()
	stored, err := server.outbounds.CloneOutbound(request.Context(), input.SourceID, input.ExpectedRevision, clone, server.now().UTC())
	if errors.Is(err, database.ErrConflict) {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Source outbound changed or clone already exists.", err))
		return
	}
	if err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid outbound clone.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusCreated, outboundDocument(stored))
}

func (server *Server) planOutboundsHandler(writer http.ResponseWriter, request *http.Request) {
	var input struct{}
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid sing-box plan request.", err))
		return
	}
	plan, err := server.control.PlanSingBox(request.Context())
	if err != nil {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "sing-box plan rejected by safety checks.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, plan)
}

func (server *Server) applyOutboundsHandler(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		ExpectedStateHash     string `json:"expected_state_hash"`
		ExpectedCandidateHash string `json:"expected_candidate_hash"`
	}
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid sing-box apply request.", err))
		return
	}
	if input.ExpectedStateHash == "" || input.ExpectedCandidateHash == "" {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Reviewed sing-box plan hashes are required.", nil))
		return
	}
	transactionID := domain.ID("singbox_" + logging.OperationID(request.Context()))
	result, err := server.control.ApplySingBox(request.Context(), managedSingBox.ApplyRequest{TransactionID: transactionID, ExpectedStateHash: input.ExpectedStateHash, ExpectedCandidateHash: input.ExpectedCandidateHash})
	if err != nil {
		WriteMutationError(writer, request, "sing-box apply failed and rollback was attempted.", err)
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}

func outboundDocument(item database.StoredOutbound) storedOutboundDocument {
	return storedOutboundDocument{Outbound: item.Outbound, Revision: item.Revision, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}
