package reliability

import (
	"context"
	"fmt"
	"time"
)

const MaximumRecoverySteps = 16

type RecoveryStep struct {
	Component string
	Recover   func(context.Context) error
}

type StepResult struct {
	Component string    `json:"component"`
	Succeeded bool      `json:"succeeded"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

type RecoveryReport struct {
	Succeeded       bool         `json:"succeeded"`
	FailedComponent string       `json:"failed_component,omitempty"`
	Steps           []StepResult `json:"steps"`
	StartedAt       time.Time    `json:"started_at"`
	EndedAt         time.Time    `json:"ended_at"`
}

type Coordinator struct {
	Steps []RecoveryStep
	Now   func() time.Time
}

func (coordinator Coordinator) Run(ctx context.Context) (RecoveryReport, error) {
	if ctx == nil {
		return RecoveryReport{}, fmt.Errorf("recovery context is required")
	}
	if len(coordinator.Steps) == 0 || len(coordinator.Steps) > MaximumRecoverySteps {
		return RecoveryReport{}, fmt.Errorf("recovery coordinator requires between 1 and %d steps", MaximumRecoverySteps)
	}
	seen := make(map[string]struct{}, len(coordinator.Steps))
	for _, step := range coordinator.Steps {
		if !componentPattern.MatchString(step.Component) || step.Recover == nil {
			return RecoveryReport{}, fmt.Errorf("recovery step is invalid")
		}
		if _, exists := seen[step.Component]; exists {
			return RecoveryReport{}, fmt.Errorf("recovery component %q is duplicated", step.Component)
		}
		seen[step.Component] = struct{}{}
	}
	now := coordinator.Now
	if now == nil {
		now = time.Now
	}
	report := RecoveryReport{StartedAt: now().UTC(), Steps: make([]StepResult, 0, len(coordinator.Steps))}
	for _, step := range coordinator.Steps {
		result := StepResult{Component: step.Component, StartedAt: now().UTC()}
		err := ctx.Err()
		if err == nil {
			err = step.Recover(ctx)
		}
		result.EndedAt = now().UTC()
		result.Succeeded = err == nil
		report.Steps = append(report.Steps, result)
		if err != nil {
			report.FailedComponent = step.Component
			report.EndedAt = result.EndedAt
			return report, fmt.Errorf("recover %s: %w", step.Component, err)
		}
	}
	report.Succeeded = true
	report.EndedAt = now().UTC()
	return report, nil
}
