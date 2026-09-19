package model

import (
	"fmt"
	"strings"
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
)

// SpecimenIsolationEvent is the append-only history entry describing one
// isolation episode of a specimen caused by a cold-chain anomaly.
type SpecimenIsolationEvent struct {
	ID                   uint                      `gorm:"primaryKey" json:"id"`
	CreatedAt            time.Time                 `json:"createdAt"`
	UpdatedAt            time.Time                 `json:"updatedAt"`
	SpecimenID           uint                      `gorm:"index;not null" json:"specimenId"`
	TemperatureAnomalyID uint                      `gorm:"index;not null" json:"temperatureAnomalyId"`
	TemperatureAnomaly   *TemperatureAnomaly       `json:"temperatureAnomaly,omitempty"`
	Reason               constants.IsolationReason `gorm:"size:40;index;not null" json:"reason"`
	ContainerID          uint                      `gorm:"index;not null" json:"containerId"`
	Container            *StorageContainer         `json:"container,omitempty"`
	Active               bool                      `gorm:"not null;default:false;index" json:"active"`
	IsolatedByID         uint                      `gorm:"index;not null" json:"isolatedById"`
	IsolatedByName       string                    `gorm:"size:100;not null" json:"isolatedByName"`
	IsolatedAt           time.Time                 `gorm:"index;not null" json:"isolatedAt"`
	ReleasedByID         *uint                     `gorm:"index" json:"releasedById,omitempty"`
	ReleasedByName       string                    `gorm:"size:100" json:"releasedByName,omitempty"`
	ReleasedAt           *time.Time                `gorm:"index" json:"releasedAt,omitempty"`
	ReleaseBasis         string                    `gorm:"size:1000" json:"releaseBasis,omitempty"`
	Outcome              string                    `gorm:"size:24;index" json:"outcome,omitempty"`
}

func (e *SpecimenIsolationEvent) Normalize() {
	e.IsolatedByName = strings.TrimSpace(e.IsolatedByName)
	e.ReleasedByName = strings.TrimSpace(e.ReleasedByName)
	e.ReleaseBasis = strings.TrimSpace(e.ReleaseBasis)
	e.Outcome = strings.TrimSpace(e.Outcome)
}

func (e SpecimenIsolationEvent) Validate() error {
	if e.SpecimenID == 0 || e.TemperatureAnomalyID == 0 || e.ContainerID == 0 {
		return fmt.Errorf("isolation event requires specimen, anomaly and container")
	}
	if !e.Reason.Valid() {
		return fmt.Errorf("unsupported isolation reason: %s", e.Reason)
	}
	if e.IsolatedByID == 0 || e.IsolatedByName == "" || e.IsolatedAt.IsZero() {
		return fmt.Errorf("isolation requires operator identity and time")
	}
	if !e.Active {
		if e.ReleasedByID == nil || *e.ReleasedByID == 0 || e.ReleasedByName == "" || e.ReleasedAt == nil {
			return fmt.Errorf("released isolation requires operator identity and time")
		}
		if len([]rune(e.ReleaseBasis)) < 5 {
			return fmt.Errorf("release basis must contain at least 5 characters")
		}
	}
	return nil
}

// TableName keeps the history table name explicit and stable.
func (SpecimenIsolationEvent) TableName() string { return "specimen_isolation_events" }
