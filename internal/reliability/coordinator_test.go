package reliability

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCoordinatorRunsInOrderAndIsIdempotent(t *testing.T) {
	t.Parallel()

	var calls []string
	counts := map[string]int{}
	steps := make([]RecoveryStep, 0, 3)
	for _, component := range []string{"outbound", "proxy", "routing"} {
		name := component
		steps = append(steps, RecoveryStep{Component: name, Recover: func(context.Context) error {
			calls = append(calls, name)
			counts[name]++
			return nil
		}})
	}
	now := time.Date(2026, 9, 5, 5, 0, 0, 0, time.UTC)
	coordinator := Coordinator{Steps: steps, Now: func() time.Time { return now }}
	for run := 0; run < 2; run++ {
		report, err := coordinator.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !report.Succeeded || report.FailedComponent != "" || len(report.Steps) != 3 {
			t.Fatalf("recovery report = %#v", report)
		}
	}
	if !reflect.DeepEqual(calls, []string{"outbound", "proxy", "routing", "outbound", "proxy", "routing"}) {
		t.Fatalf("recovery calls = %v", calls)
	}
	for component, count := range counts {
		if count != 2 {
			t.Fatalf("%s recovery count = %d", component, count)
		}
	}
}

func TestCoordinatorStopsAtFailureWithoutExposingError(t *testing.T) {
	t.Parallel()

	calledAfterFailure := false
	secret := "TEST_ONLY_SECRET_RECOVERY_DETAIL"
	coordinator := Coordinator{Steps: []RecoveryStep{
		{Component: "outbound", Recover: func(context.Context) error { return errors.New(secret) }},
		{Component: "routing", Recover: func(context.Context) error { calledAfterFailure = true; return nil }},
	}}
	report, err := coordinator.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), secret) {
		t.Fatalf("recovery error = %v", err)
	}
	if report.Succeeded || report.FailedComponent != "outbound" || len(report.Steps) != 1 || report.Steps[0].Succeeded || calledAfterFailure {
		t.Fatalf("recovery report = %#v; called after failure = %t", report, calledAfterFailure)
	}
	encoded, marshalErr := json.Marshal(report)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatal("public recovery report exposed the internal recovery error")
	}
}

func TestCoordinatorRejectsInvalidStepDefinitions(t *testing.T) {
	t.Parallel()

	for _, steps := range [][]RecoveryStep{
		nil,
		{{Component: "Invalid Component", Recover: func(context.Context) error { return nil }}},
		{{Component: "routing", Recover: nil}},
		{{Component: "routing", Recover: func(context.Context) error { return nil }}, {Component: "routing", Recover: func(context.Context) error { return nil }}},
	} {
		if _, err := (Coordinator{Steps: steps}).Run(context.Background()); err == nil {
			t.Fatalf("invalid recovery steps accepted: %#v", steps)
		}
	}
}
