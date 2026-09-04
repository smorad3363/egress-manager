package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/egress-manager/egress-manager/internal/logging"
)

type HealthCheck struct {
	Name     string
	Critical bool
	Check    func(context.Context) error
}

type HealthHandler struct {
	Version string
	Checks  []HealthCheck
	Timeout time.Duration
}

type healthDocument struct {
	Status      string            `json:"status"`
	Version     string            `json:"version"`
	OperationID string            `json:"operation_id,omitempty"`
	Checks      map[string]string `json:"checks"`
}

func (handler HealthHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		WriteError(writer, request, NewError(http.StatusMethodNotAllowed, CodeMethodNotAllow, "Method not allowed.", nil))
		return
	}
	timeout := handler.Timeout
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 2 * time.Second
	}
	checks := append([]HealthCheck(nil), handler.Checks...)
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })

	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	defer cancel()
	status := http.StatusOK
	document := healthDocument{
		Status:      "ok",
		Version:     handler.Version,
		OperationID: logging.OperationID(request.Context()),
		Checks:      make(map[string]string, len(checks)),
	}
	for _, check := range checks {
		if check.Check == nil || check.Name == "" {
			continue
		}
		if err := check.Check(ctx); err != nil {
			document.Checks[check.Name] = "failed"
			if check.Critical {
				status = http.StatusServiceUnavailable
				document.Status = "degraded"
			}
		} else {
			document.Checks[check.Name] = "ok"
		}
	}
	if request.Method == http.MethodHead {
		writer.WriteHeader(status)
		return
	}
	_ = WriteJSON(writer, status, document)
}
