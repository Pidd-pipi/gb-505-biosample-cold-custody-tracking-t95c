package model

import (
	"fmt"
	"strings"
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
)

// SpecimenQuarantine 是样本隔离状态变更的只追加历史。
// 每次因温度异常隔离或解除隔离都会留下一条记录，样本详情据此回读历史。
type SpecimenQuarantine struct {
	ID                 uint                       `gorm:"primaryKey" json:"id"`
	CreatedAt          time.Time                  `gorm:"index;not null" json:"createdAt"`
	SpecimenID         uint                       `gorm:"index;not null" json:"specimenId"`
	Specimen           *Specimen                  `gorm:"foreignKey:SpecimenID" json:"specimen,omitempty"`
	AnomalyID          uint                       `gorm:"index;not null" json:"anomalyId"`
	Anomaly            *TemperatureAnomaly        `gorm:"foreignKey:AnomalyID" json:"anomaly,omitempty"`
	Action             constants.QuarantineAction `gorm:"size:20;index;not null" json:"action"`
	StorageContainerID uint                       `gorm:"index;not null" json:"storageContainerId"`
	StorageContainer   *StorageContainer          `gorm:"foreignKey:StorageContainerID" json:"storageContainer,omitempty"`
	OperatorID         uint                       `gorm:"index;not null" json:"operatorId"`
	OperatorName       string                     `gorm:"size:100;not null" json:"operatorName"`
	Reason             string                     `gorm:"size:1000" json:"reason,omitempty"`
}

func (q *SpecimenQuarantine) Normalize() {
	q.OperatorName = strings.TrimSpace(q.OperatorName)
	q.Reason = strings.TrimSpace(q.Reason)
}

func (q SpecimenQuarantine) Validate() error {
	if q.SpecimenID == 0 || q.AnomalyID == 0 || q.StorageContainerID == 0 {
		return fmt.Errorf("specimen, anomaly and storage container are required")
	}
	if !q.Action.Valid() {
		return fmt.Errorf("unsupported quarantine action: %s", q.Action)
	}
	if q.OperatorID == 0 || q.OperatorName == "" {
		return fmt.Errorf("operator identity is required")
	}
	if len([]rune(q.Reason)) > 1000 {
		return fmt.Errorf("quarantine reason cannot exceed 1000 characters")
	}
	return nil
}
