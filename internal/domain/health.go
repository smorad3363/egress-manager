package domain

import (
	"fmt"
	"time"
)

type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
	HealthDisabled  HealthStatus = "disabled"
)

func (status HealthStatus) Validate() error {
	switch status {
	case HealthUnknown, HealthHealthy, HealthDegraded, HealthUnhealthy, HealthDisabled:
		return nil
	default:
		return fmt.Errorf("unsupported health status %q", status)
	}
}

type Health struct {
	Status    HealthStatus  `json:"status"`
	CheckedAt *time.Time    `json:"checked_at,omitempty"`
	Latency   time.Duration `json:"latency,omitempty"`
	Detail    string        `json:"detail,omitempty"`
}

func (health Health) Validate() error {
	if health.Latency < 0 {
		return fmt.Errorf("latency must not be negative")
	}
	if len(health.Detail) > 256 {
		return fmt.Errorf("health detail must not exceed 256 bytes")
	}
	return health.Status.Validate()
}
