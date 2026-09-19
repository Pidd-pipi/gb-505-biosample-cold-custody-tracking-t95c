package dto

import (
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
)

type CreateTemperatureAnomalyRequest struct {
	StorageContainerID uint      `json:"storageContainerId" binding:"required"`
	RecordedC          float64   `json:"recordedC" binding:"gte=-210,lte=40"`
	ReadingAt          time.Time `json:"readingAt" binding:"required"`
	Description        string    `json:"description" binding:"max=1000"`
}

type ResolveTemperatureAnomalyRequest struct {
	Decision        constants.TemperatureAnomalyState `json:"decision" binding:"required,oneof=resolved invalid"`
	RecoveredC      *float64                          `json:"recoveredC" binding:"omitempty,gte=-210,lte=40"`
	ResolutionBasis string                            `json:"resolutionBasis" binding:"required,min=5,max=1000"`
}
