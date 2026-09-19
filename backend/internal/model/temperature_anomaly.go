package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
)

var anomalyNumberPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{2,49}$`)

// TemperatureAnomaly is the single open ticket per storage container while its
// cold-chain readings are out of range. Specimens stored when the ticket is
// opened are isolated until the ticket is closed.
type TemperatureAnomaly struct {
	Base
	AnomalyNo          string                            `gorm:"size:50;uniqueIndex;not null" json:"anomalyNo"`
	StorageContainerID uint                              `gorm:"index;not null" json:"storageContainerId"`
	StorageContainer   *StorageContainer                 `json:"storageContainer,omitempty"`
	RecordedC          float64                           `gorm:"type:numeric(6,2);not null" json:"recordedC"`
	ReadingAt          time.Time                         `gorm:"index;not null" json:"readingAt"`
	State              constants.TemperatureAnomalyState `gorm:"size:20;index;not null;default:'open'" json:"state"`
	Description        string                            `gorm:"size:1000" json:"description,omitempty"`
	ReportedByID       uint                              `gorm:"index;not null" json:"reportedById"`
	ReportedByName     string                            `gorm:"size:100;not null" json:"reportedByName"`
	ReportedAt         time.Time                         `gorm:"index;not null" json:"reportedAt"`
	ResolvedByID       *uint                             `gorm:"index" json:"resolvedById,omitempty"`
	ResolvedByName     string                            `gorm:"size:100" json:"resolvedByName,omitempty"`
	ResolvedAt         *time.Time                        `gorm:"index" json:"resolvedAt,omitempty"`
	RecoveredC         *float64                          `gorm:"type:numeric(6,2)" json:"recoveredC,omitempty"`
	ResolutionBasis    string                            `gorm:"size:1000" json:"resolutionBasis,omitempty"`
	IsolatedCount      int                               `gorm:"not null;default:0" json:"isolatedCount"`
	IsolationEvents    []SpecimenIsolationEvent          `json:"isolationEvents,omitempty"`
}

func (a *TemperatureAnomaly) Normalize() {
	a.AnomalyNo = strings.ToUpper(strings.TrimSpace(a.AnomalyNo))
	a.Description = strings.TrimSpace(a.Description)
	a.ReportedByName = strings.TrimSpace(a.ReportedByName)
	a.ResolvedByName = strings.TrimSpace(a.ResolvedByName)
	a.ResolutionBasis = strings.TrimSpace(a.ResolutionBasis)
	if a.State == "" {
		a.State = constants.AnomalyStateOpen
	}
}

func (a TemperatureAnomaly) Validate() error {
	if !anomalyNumberPattern.MatchString(a.AnomalyNo) {
		return fmt.Errorf("anomaly number must contain 3-50 uppercase letters, numbers or hyphens")
	}
	if a.StorageContainerID == 0 {
		return fmt.Errorf("storage container is required")
	}
	if a.RecordedC < -210 || a.RecordedC > 40 {
		return fmt.Errorf("recorded temperature must be between -210 and 40 Celsius")
	}
	if a.ReadingAt.IsZero() {
		return fmt.Errorf("reading time is required")
	}
	if !a.State.ValidLifecycle() {
		return fmt.Errorf("unsupported anomaly state: %s", a.State)
	}
	if a.ReportedByID == 0 || a.ReportedByName == "" || a.ReportedAt.IsZero() {
		return fmt.Errorf("reporter identity and time are required")
	}
	if len([]rune(a.Description)) > 1000 {
		return fmt.Errorf("description cannot exceed 1000 characters")
	}
	if a.State == constants.AnomalyStateOpen {
		if a.ResolvedByID != nil || a.ResolvedAt != nil || a.ResolutionBasis != "" {
			return fmt.Errorf("open anomaly cannot contain resolution metadata")
		}
		return nil
	}
	if a.ResolvedByID == nil || *a.ResolvedByID == 0 || a.ResolvedByName == "" || a.ResolvedAt == nil {
		return fmt.Errorf("resolved anomaly requires resolver identity and time")
	}
	if len([]rune(a.ResolutionBasis)) < 5 {
		return fmt.Errorf("resolution basis must contain at least 5 characters")
	}
	return nil
}

func (a TemperatureAnomaly) IsOpen() bool {
	return a.State == constants.AnomalyStateOpen
}
