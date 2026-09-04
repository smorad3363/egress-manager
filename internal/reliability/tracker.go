package reliability

import "sync"

// Tracker keeps the current daemon's sanitized recovery outcome available even
// when recovery fails before ordinary mutation service can become ready.
type Tracker struct {
	mu   sync.RWMutex
	last *RecoveryReport
}

func (tracker *Tracker) Record(report RecoveryReport) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	report.Steps = append([]StepResult{}, report.Steps...)
	tracker.last = &report
}

func (tracker *Tracker) Last() *RecoveryReport {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	if tracker.last == nil {
		return nil
	}
	report := *tracker.last
	report.Steps = append([]StepResult{}, report.Steps...)
	return &report
}

func (tracker *Tracker) Ready() bool {
	report := tracker.Last()
	return report != nil && report.Succeeded
}

type RecoveryRequiredError struct{}

func (RecoveryRequiredError) Error() string        { return "host recovery is required before mutation" }
func (RecoveryRequiredError) IPCErrorCode() string { return "recovery_required" }
