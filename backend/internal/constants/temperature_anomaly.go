package constants

import "fmt"

// TemperatureAnomalyState tracks the cold-chain anomaly ticket lifecycle.
type TemperatureAnomalyState string

const (
	AnomalyStateOpen       TemperatureAnomalyState = "open"
	AnomalyStateResolved   TemperatureAnomalyState = "resolved"
	AnomalyStateInvalid    TemperatureAnomalyState = "invalid"
	AnomalyResolutionClear TemperatureAnomalyState = "clear"
)

func (s TemperatureAnomalyState) ValidLifecycle() bool {
	switch s {
	case AnomalyStateOpen, AnomalyStateResolved, AnomalyStateInvalid:
		return true
	default:
		return false
	}
}

func (s TemperatureAnomalyState) Open() bool {
	return s == AnomalyStateOpen
}

// ValidateAnomalyResolution ensures a patrol decision can close an open anomaly.
func ValidateAnomalyResolution(decision TemperatureAnomalyState) error {
	switch decision {
	case AnomalyStateResolved, AnomalyStateInvalid:
		return nil
	default:
		return fmt.Errorf("invalid anomaly resolution decision: %s", decision)
	}
}

// IsolationReason describes why a specimen entered isolation.
type IsolationReason string

const (
	IsolationReasonTemperature IsolationReason = "temperature_excursion"
)

func (r IsolationReason) Valid() bool {
	switch r {
	case IsolationReasonTemperature:
		return true
	default:
		return false
	}
}
