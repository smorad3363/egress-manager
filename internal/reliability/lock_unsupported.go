//go:build !linux

package reliability

import (
	"fmt"

	"github.com/egress-manager/egress-manager/internal/domain"
)

func (lock FileLock) TryAcquire(domain.ID, string) (*Lease, error) {
	return nil, fmt.Errorf("operation locking requires Linux")
}

func (lock FileLock) Inspect() (Status, error) {
	return Status{}, fmt.Errorf("operation locking requires Linux")
}

func writeOwner(lockFile, Owner) error {
	return fmt.Errorf("operation locking requires Linux")
}

func unlockFile(lockFile) error {
	return fmt.Errorf("operation locking requires Linux")
}
