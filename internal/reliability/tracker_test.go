package reliability

import (
	"sync"
	"testing"
)

func TestTrackerCopiesReportsAndHandlesConcurrentReaders(t *testing.T) {
	tracker := &Tracker{}
	if tracker.Ready() || tracker.Last() != nil {
		t.Fatal("uninitialized tracker is ready")
	}
	report := RecoveryReport{Succeeded: true, Steps: []StepResult{{Component: "journal", Succeeded: true}}}
	tracker.Record(report)
	report.Steps[0].Component = "changed"
	copy := tracker.Last()
	copy.Steps[0].Component = "changed_again"
	if tracker.Last().Steps[0].Component != "journal" || !tracker.Ready() {
		t.Fatal("report storage is aliased")
	}
	var group sync.WaitGroup
	for range 10 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 20 {
				tracker.Record(RecoveryReport{Succeeded: true})
				_ = tracker.Last()
				_ = tracker.Ready()
			}
		}()
	}
	group.Wait()
	tracker.Record(RecoveryReport{FailedComponent: "routing"})
	if tracker.Ready() {
		t.Fatal("failed recovery is ready")
	}
}
