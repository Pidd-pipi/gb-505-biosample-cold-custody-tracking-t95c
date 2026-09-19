package constants

import "fmt"

// AnomalyState 是冷链温度异常单的生命周期状态。
// open 表示未结异常（隔离中），released 表示已由非建单人填写依据后解除。
type AnomalyState string

const (
	AnomalyStateOpen     AnomalyState = "open"
	AnomalyStateReleased AnomalyState = "released"
)

func anomalyStates() []AnomalyState {
	return []AnomalyState{AnomalyStateOpen, AnomalyStateReleased}
}

func (s AnomalyState) Valid() bool {
	for _, candidate := range anomalyStates() {
		if s == candidate {
			return true
		}
	}
	return false
}

func (s AnomalyState) Open() bool {
	return s == AnomalyStateOpen
}

// CanReleaseTo 仅允许未结异常解除为 released，解除不可逆向、不可重复。
func (s AnomalyState) CanReleaseTo(next AnomalyState) error {
	if s != AnomalyStateOpen {
		return fmt.Errorf("只有未结异常可以解除")
	}
	if next != AnomalyStateReleased {
		return fmt.Errorf("异常单只能解除为 released")
	}
	return nil
}

// QuarantineAction 描述样本隔离历史的动作类型。
type QuarantineAction string

const (
	// QuarantineActionIsolated 表示样本因温度异常被转入隔离。
	QuarantineActionIsolated QuarantineAction = "isolated"
	// QuarantineActionReleased 表示异常解除后样本恢复正常。
	QuarantineActionReleased QuarantineAction = "released"
)

func (a QuarantineAction) Valid() bool {
	switch a {
	case QuarantineActionIsolated, QuarantineActionReleased:
		return true
	default:
		return false
	}
}
