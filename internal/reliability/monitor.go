package reliability

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

const (
	MaximumDependencyProbes = 16
	maximumHealthDetail     = 128
	defaultProbeTimeout     = 5 * time.Second
	defaultMonitorInterval  = 30 * time.Second
	failureThreshold        = 2
	recoveryThreshold       = 2
)

type DependencyObservation struct {
	Status              domain.HealthStatus `json:"status"`
	ExecutableAvailable domain.ProbeStatus  `json:"executable_available"`
	ConfigurationValid  domain.ProbeStatus  `json:"configuration_valid"`
	ProcessRunning      domain.ProbeStatus  `json:"process_running"`
	TransportReachable  domain.ProbeStatus  `json:"transport_reachable"`
	InternetReachable   domain.ProbeStatus  `json:"internet_reachable"`
	Detail              string              `json:"detail,omitempty"`
}

func UnknownDependencyObservation() DependencyObservation {
	return DependencyObservation{
		Status:              domain.HealthUnknown,
		ExecutableAvailable: domain.ProbeUnknown,
		ConfigurationValid:  domain.ProbeUnknown,
		ProcessRunning:      domain.ProbeUnknown,
		TransportReachable:  domain.ProbeUnknown,
		InternetReachable:   domain.ProbeUnknown,
	}
}

func (observation DependencyObservation) Validate() error {
	if len(observation.Detail) > maximumHealthDetail {
		return fmt.Errorf("dependency health detail exceeds %d bytes", maximumHealthDetail)
	}
	if observation.Status == domain.HealthDisabled {
		return observation.Status.Validate()
	}
	return errorsJoin(
		observation.Status.Validate(),
		observation.ExecutableAvailable.Validate(),
		observation.ConfigurationValid.Validate(),
		observation.ProcessRunning.Validate(),
		observation.TransportReachable.Validate(),
		observation.InternetReachable.Validate(),
	)
}

type DependencyProbe struct {
	Name  string
	Check func(context.Context) (DependencyObservation, error)
}

type DependencyStatus struct {
	Name string `json:"name"`
	DependencyObservation
	LastChecked         time.Time  `json:"last_checked"`
	LastSuccess         *time.Time `json:"last_success,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
}

type dependencyState struct {
	status               DependencyStatus
	consecutiveSuccesses int
}

// Monitor is observational. It never invokes mutation or reconciliation paths.
type Monitor struct {
	Probes   []DependencyProbe
	Timeout  time.Duration
	Interval time.Duration
	Now      func() time.Time

	mu     sync.RWMutex
	states map[string]dependencyState
}

func (monitor *Monitor) Validate() error {
	if monitor == nil || len(monitor.Probes) == 0 || len(monitor.Probes) > MaximumDependencyProbes {
		return fmt.Errorf("dependency monitor requires between 1 and %d probes", MaximumDependencyProbes)
	}
	seen := make(map[string]struct{}, len(monitor.Probes))
	for _, probe := range monitor.Probes {
		if !componentPattern.MatchString(probe.Name) || probe.Check == nil {
			return fmt.Errorf("dependency probe is invalid")
		}
		if _, exists := seen[probe.Name]; exists {
			return fmt.Errorf("dependency probe %q is duplicated", probe.Name)
		}
		seen[probe.Name] = struct{}{}
	}
	return nil
}

func (monitor *Monitor) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("dependency monitor context is required")
	}
	if err := monitor.Validate(); err != nil {
		return err
	}
	monitor.RunOnce(ctx)
	ticker := time.NewTicker(monitor.interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			monitor.RunOnce(ctx)
		}
	}
}

func (monitor *Monitor) RunOnce(ctx context.Context) {
	if ctx == nil || monitor.Validate() != nil {
		return
	}
	type result struct {
		name        string
		observation DependencyObservation
	}
	results := make(chan result, len(monitor.Probes))
	var wait sync.WaitGroup
	for _, probe := range monitor.Probes {
		probe := probe
		wait.Add(1)
		go func() {
			defer wait.Done()
			probeContext, cancel := context.WithTimeout(ctx, monitor.timeout())
			defer cancel()
			checked := make(chan result, 1)
			go func() {
				observation, err := probe.Check(probeContext)
				if err != nil {
					observation = failedDependencyObservation("probe_failed")
				} else if observation.Validate() != nil {
					observation = failedDependencyObservation("invalid_probe_result")
				}
				checked <- result{name: probe.Name, observation: observation}
			}()
			select {
			case item := <-checked:
				results <- item
			case <-probeContext.Done():
				results <- result{name: probe.Name, observation: failedDependencyObservation("probe_timeout")}
			}
		}()
	}
	wait.Wait()
	close(results)
	now := monitor.now()
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if monitor.states == nil {
		monitor.states = make(map[string]dependencyState, len(monitor.Probes))
	}
	for item := range results {
		state := monitor.states[item.name]
		previousStatus := state.status.Status
		state.status.Name = item.name
		state.status.DependencyObservation = item.observation
		state.status.LastChecked = now
		switch item.observation.Status {
		case domain.HealthHealthy:
			state.status.ConsecutiveFailures = 0
			state.consecutiveSuccesses++
			success := now
			state.status.LastSuccess = &success
			if previousStatus == domain.HealthUnhealthy && state.consecutiveSuccesses < recoveryThreshold {
				state.status.Status = domain.HealthDegraded
				state.status.Detail = "recovering"
			}
		case domain.HealthUnhealthy:
			state.consecutiveSuccesses = 0
			state.status.ConsecutiveFailures++
			if state.status.ConsecutiveFailures < failureThreshold {
				state.status.Status = domain.HealthDegraded
			}
		case domain.HealthDegraded:
			state.consecutiveSuccesses = 0
			state.status.ConsecutiveFailures++
		case domain.HealthDisabled:
			state.consecutiveSuccesses = 0
			state.status.ConsecutiveFailures = 0
		default:
			state.consecutiveSuccesses = 0
		}
		monitor.states[item.name] = state
	}
}

func (monitor *Monitor) Snapshot() []DependencyStatus {
	if monitor == nil {
		return []DependencyStatus{}
	}
	monitor.mu.RLock()
	defer monitor.mu.RUnlock()
	result := make([]DependencyStatus, 0, len(monitor.states))
	for _, state := range monitor.states {
		status := state.status
		if status.LastSuccess != nil {
			lastSuccess := *status.LastSuccess
			status.LastSuccess = &lastSuccess
		}
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func failedDependencyObservation(detail string) DependencyObservation {
	observation := UnknownDependencyObservation()
	observation.Status = domain.HealthUnhealthy
	observation.Detail = detail
	return observation
}

func (monitor *Monitor) timeout() time.Duration {
	if monitor.Timeout <= 0 || monitor.Timeout > 30*time.Second {
		return defaultProbeTimeout
	}
	return monitor.Timeout
}

func (monitor *Monitor) interval() time.Duration {
	if monitor.Interval < time.Second || monitor.Interval > 5*time.Minute {
		return defaultMonitorInterval
	}
	return monitor.Interval
}

func (monitor *Monitor) now() time.Time {
	if monitor.Now != nil {
		return monitor.Now().UTC()
	}
	return time.Now().UTC()
}

func errorsJoin(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
