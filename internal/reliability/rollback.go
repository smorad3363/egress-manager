package reliability

import (
	"errors"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

var ErrNoRollbackAvailable = errors.New("no eligible rollback snapshot is available")

type NoRollbackAvailableError struct{}

func (NoRollbackAvailableError) Error() string        { return ErrNoRollbackAvailable.Error() }
func (NoRollbackAvailableError) IPCErrorCode() string { return "no_rollback_available" }

type RollbackReport struct {
	Succeeded   bool      `json:"succeeded"`
	Component   string    `json:"component"`
	OperationID domain.ID `json:"operation_id"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
}
