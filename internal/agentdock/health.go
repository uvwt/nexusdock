package agentdock

import "time"

type HealthState string

const (
	HealthReady        HealthState = "ready"
	HealthOffline      HealthState = "offline"
	HealthDisabled     HealthState = "disabled"
	HealthIncompatible HealthState = "incompatible"
)

type Health struct {
	State              HealthState
	Online             bool
	Enabled            bool
	Compatible         bool
	LastSeenAt         *time.Time
	LastSeenAgeSeconds int64
}

func EvaluateHealth(node Node, online bool, compatibility Compatibility, now time.Time) Health {
	health := Health{
		Online: online, Enabled: node.Enabled, Compatible: compatibility.Compatible,
		LastSeenAt: node.LastSeenAt,
	}
	if node.LastSeenAt != nil {
		age := now.UTC().Sub(node.LastSeenAt.UTC())
		if age < 0 {
			age = 0
		}
		health.LastSeenAgeSeconds = int64(age / time.Second)
	}
	switch {
	case !node.Enabled:
		health.State = HealthDisabled
	case !compatibility.Compatible:
		health.State = HealthIncompatible
	case online:
		health.State = HealthReady
	default:
		health.State = HealthOffline
	}
	return health
}
