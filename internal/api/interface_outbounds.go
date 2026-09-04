package api

import (
	"net/http"

	"github.com/egress-manager/egress-manager/internal/domain"
	managedInterface "github.com/egress-manager/egress-manager/internal/interfaceoutbound"
	"github.com/egress-manager/egress-manager/internal/logging"
)

const maximumInterfaceImportJSONBytes = managedInterface.MaximumImportBytes + 1024

func (server *Server) interfaceOutboundImportHandler(writer http.ResponseWriter, request *http.Request) {
	var input managedInterface.ImportRequest
	if err := decodeJSONLimit(request, &input, maximumInterfaceImportJSONBytes); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid interface outbound import.", err))
		return
	}
	result, err := server.control.ImportInterfaceOutbound(request.Context(), input)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Interface outbound import rejected.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusCreated, result)
}

func (server *Server) interfaceOutboundTestHandler(writer http.ResponseWriter, request *http.Request) {
	var input managedInterface.TestRequest
	if err := decodeJSONLimit(request, &input, maximumInterfaceImportJSONBytes); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid interface outbound health request.", err))
		return
	}
	if (input.ID == "") == (input.Input == "") || input.ID != "" && input.ExpectedRevision < 1 {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Exactly one stored interface outbound or profile is required.", nil))
		return
	}
	result, err := server.control.TestInterfaceOutbound(request.Context(), input)
	if err != nil {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Interface outbound health check rejected.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}

func (server *Server) interfaceOutboundsPlanHandler(writer http.ResponseWriter, request *http.Request) {
	var input struct{}
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid interface outbound plan request.", err))
		return
	}
	result, err := server.control.PlanInterfaceOutbounds(request.Context())
	if err != nil {
		WriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Interface outbound plan rejected by safety checks.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}

func (server *Server) interfaceOutboundsApplyHandler(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		ExpectedStateHash     string `json:"expected_state_hash"`
		ExpectedCandidateHash string `json:"expected_candidate_hash"`
	}
	if err := decodeJSON(request, &input); err != nil {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Invalid interface outbound apply request.", err))
		return
	}
	if input.ExpectedStateHash == "" || input.ExpectedCandidateHash == "" {
		WriteError(writer, request, NewError(http.StatusBadRequest, CodeBadRequest, "Reviewed interface outbound hashes are required.", nil))
		return
	}
	transactionID := "interface_" + logging.OperationID(request.Context())
	result, err := server.control.ApplyInterfaceOutbounds(request.Context(), managedInterface.ApplyRequest{TransactionID: domain.ID(transactionID), ExpectedStateHash: input.ExpectedStateHash, ExpectedCandidateHash: input.ExpectedCandidateHash})
	if err != nil {
		WriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "Interface outbound apply failed and rollback was attempted.", err))
		return
	}
	_ = WriteJSON(writer, http.StatusOK, result)
}
