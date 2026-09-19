package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
)

var anomalyNumberPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{2,49}$`)

// TemperatureAnomaly 记录一次冻存容器温度超限巡检事件及其闭环状态。
// 同一冻存容器至多存在一张 open 异常单，由数据库部分唯一索引兜底。
type TemperatureAnomaly struct {
	Base
	AnomalyNo          string                 `gorm:"size:50;uniqueIndex;not null" json:"anomalyNo"`
	StorageContainerID uint                   `gorm:"index;not null" json:"storageContainerId"`
	StorageContainer   *StorageContainer      `gorm:"foreignKey:StorageContainerID" json:"storageContainer,omitempty"`
	TemperatureC       float64                `gorm:"type:numeric(6,2);not null" json:"temperatureC"`
	ZoneLowerC         float64                `gorm:"type:numeric(6,2);not null" json:"zoneLowerC"`
	ZoneUpperC         float64                `gorm:"type:numeric(6,2);not null" json:"zoneUpperC"`
	InspectedAt        time.Time              `gorm:"index;not null" json:"inspectedAt"`
	Description        string                 `gorm:"size:1000" json:"description,omitempty"`
	Status             constants.AnomalyState `gorm:"size:20;index;not null;default:'open'" json:"status"`
	AffectedCount      int                    `gorm:"not null;default:0" json:"affectedCount"`
	ReportedByID       uint                   `gorm:"index;not null" json:"reportedById"`
	ReportedByName     string                 `gorm:"size:100;not null" json:"reportedByName"`
	ReleasedByID       *uint                  `gorm:"index" json:"releasedById,omitempty"`
	ReleasedByName     string                 `gorm:"size:100" json:"releasedByName,omitempty"`
	ReleaseBasis       string                 `gorm:"size:1000" json:"releaseBasis,omitempty"`
	ReleasedAt         *time.Time             `gorm:"index" json:"releasedAt,omitempty"`
	QuarantineEvents   []SpecimenQuarantine   `gorm:"foreignKey:AnomalyID" json:"quarantineEvents,omitempty"`
}

func (a *TemperatureAnomaly) Normalize() {
	a.AnomalyNo = strings.ToUpper(strings.TrimSpace(a.AnomalyNo))
	a.ReportedByName = strings.TrimSpace(a.ReportedByName)
	a.ReleasedByName = strings.TrimSpace(a.ReleasedByName)
	a.Description = strings.TrimSpace(a.Description)
	a.ReleaseBasis = strings.TrimSpace(a.ReleaseBasis)
	if a.Status == "" {
		a.Status = constants.AnomalyStateOpen
	}
}

func (a TemperatureAnomaly) Validate() error {
	if !anomalyNumberPattern.MatchString(a.AnomalyNo) {
		return fmt.Errorf("anomaly number must contain 3-50 uppercase letters, numbers or hyphens")
	}
	if a.StorageContainerID == 0 {
		return fmt.Errorf("storage container is required")
	}
	if !a.Status.Valid() {
		return fmt.Errorf("unsupported anomaly status: %s", a.Status)
	}
	if a.TemperatureC < -210 || a.TemperatureC > 40 {
		return fmt.Errorf("temperature must be between -210 and 40 Celsius")
	}
	if a.ZoneLowerC >= a.ZoneUpperC {
		return fmt.Errorf("temperature zone range is invalid")
	}
	if a.InspectedAt.IsZero() {
		return fmt.Errorf("inspected time is required")
	}
	if len([]rune(a.Description)) > 1000 {
		return fmt.Errorf("description cannot exceed 1000 characters")
	}
	if a.ReportedByID == 0 || a.ReportedByName == "" {
		return fmt.Errorf("reporter identity is required")
	}
	if a.AffectedCount < 0 {
		return fmt.Errorf("affected count cannot be negative")
	}
	if a.Status == constants.AnomalyStateReleased {
		if a.ReleasedByID == nil || *a.ReleasedByID == 0 || a.ReleasedByName == "" ||
			a.ReleaseBasis == "" || a.ReleasedAt == nil {
			return fmt.Errorf("released anomaly requires resolver identity, basis and time")
		}
	}
	return nil
}

// Open 报告异常是否尚未解除。
func (a TemperatureAnomaly) Open() bool {
	return a.Status == constants.AnomalyStateOpen
}
