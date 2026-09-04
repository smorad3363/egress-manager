// Package reliability coordinates cross-component recovery and host mutations.
package reliability

import (
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

const (
	lockSchemaVersion       = 1
	maximumLockMetadataSize = 4096
)

var (
	ErrBusy          = errors.New("host mutation is already in progress")
	componentPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

type Owner struct {
	SchemaVersion int        `json:"schema_version"`
	PID           int        `json:"pid"`
	ProcessStart  string     `json:"process_start"`
	OperationID   domain.ID  `json:"operation_id"`
	Component     string     `json:"component"`
	AcquiredAt    time.Time  `json:"acquired_at"`
	ReleasedAt    *time.Time `json:"released_at,omitempty"`
}

func (owner Owner) validate() error {
	if owner.SchemaVersion != lockSchemaVersion {
		return fmt.Errorf("unsupported operation lock schema version")
	}
	if owner.PID < 1 {
		return fmt.Errorf("operation lock PID is invalid")
	}
	if owner.ProcessStart == "" || len(owner.ProcessStart) > 160 {
		return fmt.Errorf("operation lock process identity is invalid")
	}
	if err := owner.OperationID.Validate("operation lock operation ID"); err != nil {
		return err
	}
	if !componentPattern.MatchString(owner.Component) {
		return fmt.Errorf("operation lock component is invalid")
	}
	if owner.AcquiredAt.IsZero() {
		return fmt.Errorf("operation lock acquisition time is required")
	}
	if owner.ReleasedAt != nil && owner.ReleasedAt.Before(owner.AcquiredAt) {
		return fmt.Errorf("operation lock release precedes acquisition")
	}
	return nil
}

type Status struct {
	Active bool   `json:"active"`
	Owner  *Owner `json:"owner,omitempty"`
}

type FileLock struct {
	Path         string
	Now          func() time.Time
	PID          int
	ProcessStart string
}

type Lease struct {
	file  lockFile
	owner Owner
	now   func() time.Time
	once  sync.Once
	err   error
}

func (lease *Lease) Owner() Owner {
	if lease == nil {
		return Owner{}
	}
	return lease.owner
}

func (lease *Lease) Release() error {
	if lease == nil {
		return nil
	}
	lease.once.Do(func() {
		releasedAt := lease.now().UTC()
		lease.owner.ReleasedAt = &releasedAt
		writeErr := writeOwner(lease.file, lease.owner)
		unlockErr := unlockFile(lease.file)
		closeErr := lease.file.Close()
		lease.err = errors.Join(writeErr, unlockErr, closeErr)
	})
	return lease.err
}

type BusyError struct {
	Owner Owner
}

func (err *BusyError) Error() string {
	if err == nil || err.Owner.OperationID == "" {
		return ErrBusy.Error()
	}
	return fmt.Sprintf("%s: %s operation %s", ErrBusy, err.Owner.Component, err.Owner.OperationID)
}

func (err *BusyError) Unwrap() error {
	return ErrBusy
}

type lockFile interface {
	Close() error
	Fd() uintptr
	Read(data []byte) (int, error)
	Seek(offset int64, whence int) (int64, error)
	Sync() error
	Truncate(size int64) error
	Write(data []byte) (int, error)
}
