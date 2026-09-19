package dto

import "time"

// CreateAnomalyRequest 为巡检员提交超限读数的请求。
// 不使用 required 校验温度，因为 0.0 对全部冻存温区都属于超限读数；是否越界由服务层判断。
type CreateAnomalyRequest struct {
	StorageContainerID uint       `json:"storageContainerId" binding:"required"`
	TemperatureC       float64    `json:"temperatureC" binding:"gte=-210,lte=40"`
	InspectedAt        *time.Time `json:"inspectedAt"`
	Description        string     `json:"description" binding:"omitempty,max=1000"`
}

// ReleaseAnomalyRequest 为非建单人解除异常时填写的依据。
type ReleaseAnomalyRequest struct {
	ReleaseBasis string `json:"releaseBasis" binding:"required,min=3,max=1000"`
}
