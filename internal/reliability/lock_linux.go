//go:build linux

package reliability

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
	"golang.org/x/sys/unix"
)

func (lock FileLock) TryAcquire(operationID domain.ID, component string) (*Lease, error) {
	if err := operationID.Validate("operation lock operation ID"); err != nil {
		return nil, err
	}
	if !componentPattern.MatchString(component) {
		return nil, fmt.Errorf("operation lock component is invalid")
	}
	if !filepath.IsAbs(lock.Path) {
		return nil, fmt.Errorf("operation lock path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(lock.Path), 0o700); err != nil {
		return nil, fmt.Errorf("create operation lock directory: %w", err)
	}
	fileDescriptor, err := unix.Open(lock.Path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open operation lock: %w", err)
	}
	file := os.NewFile(uintptr(fileDescriptor), lock.Path)
	if file == nil {
		_ = unix.Close(fileDescriptor)
		return nil, fmt.Errorf("open operation lock file")
	}
	if err := unix.Flock(fileDescriptor, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		owner, _ := readOwner(file)
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, &BusyError{Owner: owner}
		}
		return nil, fmt.Errorf("acquire operation lock: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = unix.Flock(fileDescriptor, unix.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("operation lock must be a regular file")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = unix.Flock(fileDescriptor, unix.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("secure operation lock permissions: %w", err)
	}
	now := lock.Now
	if now == nil {
		now = time.Now
	}
	pid := lock.PID
	if pid == 0 {
		pid = os.Getpid()
	}
	processStart := lock.ProcessStart
	if processStart == "" {
		processStart, err = linuxProcessIdentity(pid)
		if err != nil {
			_ = unix.Flock(fileDescriptor, unix.LOCK_UN)
			_ = file.Close()
			return nil, err
		}
	}
	owner := Owner{SchemaVersion: lockSchemaVersion, PID: pid, ProcessStart: processStart, OperationID: operationID, Component: component, AcquiredAt: now().UTC()}
	if err := owner.validate(); err != nil {
		_ = unix.Flock(fileDescriptor, unix.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	if err := writeOwner(file, owner); err != nil {
		_ = unix.Flock(fileDescriptor, unix.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	return &Lease{file: file, owner: owner, now: now}, nil
}

func (lock FileLock) Inspect() (Status, error) {
	if !filepath.IsAbs(lock.Path) {
		return Status{}, fmt.Errorf("operation lock path must be absolute")
	}
	fileDescriptor, err := unix.Open(lock.Path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, fmt.Errorf("open operation lock: %w", err)
	}
	file := os.NewFile(uintptr(fileDescriptor), lock.Path)
	if file == nil {
		_ = unix.Close(fileDescriptor)
		return Status{}, fmt.Errorf("open operation lock file")
	}
	defer file.Close()
	owner, ownerErr := readOwner(file)
	if err := unix.Flock(fileDescriptor, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			if ownerErr != nil {
				return Status{Active: true}, nil
			}
			return Status{Active: true, Owner: &owner}, nil
		}
		return Status{}, fmt.Errorf("inspect operation lock: %w", err)
	}
	if err := unix.Flock(fileDescriptor, unix.LOCK_UN); err != nil {
		return Status{}, fmt.Errorf("release operation lock inspection: %w", err)
	}
	if ownerErr != nil {
		return Status{}, ownerErr
	}
	return Status{Owner: &owner}, nil
}

func writeOwner(file lockFile, owner Owner) error {
	if err := owner.validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(owner)
	if err != nil {
		return fmt.Errorf("encode operation lock owner: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate operation lock metadata: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek operation lock metadata: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		return fmt.Errorf("write operation lock metadata: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync operation lock metadata: %w", err)
	}
	return nil
}

func readOwner(file lockFile) (Owner, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Owner{}, fmt.Errorf("seek operation lock metadata: %w", err)
	}
	content, err := io.ReadAll(io.LimitReader(file, maximumLockMetadataSize+1))
	if err != nil {
		return Owner{}, fmt.Errorf("read operation lock metadata: %w", err)
	}
	if len(content) > maximumLockMetadataSize {
		return Owner{}, fmt.Errorf("operation lock metadata exceeds %d bytes", maximumLockMetadataSize)
	}
	var owner Owner
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&owner); err != nil {
		return Owner{}, fmt.Errorf("decode operation lock metadata: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Owner{}, fmt.Errorf("decode operation lock metadata: trailing data")
	}
	if err := owner.validate(); err != nil {
		return Owner{}, err
	}
	return owner, nil
}

func unlockFile(file lockFile) error {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
		return fmt.Errorf("release operation lock: %w", err)
	}
	return nil
}

func linuxProcessIdentity(pid int) (string, error) {
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("read Linux boot identity: %w", err)
	}
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", fmt.Errorf("read process start identity: %w", err)
	}
	closing := strings.LastIndexByte(string(stat), ')')
	if closing < 0 {
		return "", fmt.Errorf("parse process start identity")
	}
	fields := strings.Fields(string(stat[closing+1:]))
	if len(fields) <= 19 {
		return "", fmt.Errorf("parse process start identity")
	}
	startTicks := fields[19]
	if _, err := strconv.ParseUint(startTicks, 10, 64); err != nil {
		return "", fmt.Errorf("parse process start identity: %w", err)
	}
	return strings.TrimSpace(string(bootID)) + ":" + startTicks, nil
}
