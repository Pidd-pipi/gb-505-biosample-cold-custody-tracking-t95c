package model

import (
	"testing"
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
)

func validAnomaly() TemperatureAnomaly {
	return TemperatureAnomaly{
		AnomalyNo:          "TA-20260919-001",
		StorageContainerID: 2,
		RecordedC:          -40.0,
		ReadingAt:          time.Now().Add(-time.Minute),
		State:              constants.AnomalyStateOpen,
		Description:        "柜门未关严，温度回升",
		ReportedByID:       3,
		ReportedByName:     "冻存保管员",
		ReportedAt:         time.Now(),
	}
}

func TestTemperatureAnomalyOpenValidation(t *testing.T) {
	anomaly := validAnomaly()
	if err := anomaly.Validate(); err != nil {
		t.Fatalf("valid open anomaly rejected: %v", err)
	}
	resolvedBy := uint(4)
	now := time.Now()
	anomaly.State = constants.AnomalyStateResolved
	anomaly.ResolvedByID = &resolvedBy
	anomaly.ResolvedByName = "另一名保管员"
	anomaly.ResolvedAt = &now
	anomaly.ResolutionBasis = "连续两小时读数恢复至 -78°C"
	if err := anomaly.Validate(); err != nil {
		t.Fatalf("valid resolved anomaly rejected: %v", err)
	}
}

func TestResolvedAnomalyRequiresBasisAndResolver(t *testing.T) {
	anomaly := validAnomaly()
	resolvedBy := uint(4)
	now := time.Now()
	anomaly.State = constants.AnomalyStateResolved
	anomaly.ResolvedByID = &resolvedBy
	anomaly.ResolvedAt = &now
	if err := anomaly.Validate(); err == nil {
		t.Fatal("resolved anomaly without resolver name and basis must fail")
	}
	anomaly.ResolvedByName = "另一名保管员"
	anomaly.ResolutionBasis = "ok"
	if err := anomaly.Validate(); err == nil {
		t.Fatal("resolution basis shorter than 5 characters must fail")
	}
}

func TestOpenAnomalyRejectsResolutionMetadata(t *testing.T) {
	anomaly := validAnomaly()
	resolver := uint(4)
	anomaly.ResolvedByID = &resolver
	if err := anomaly.Validate(); err == nil {
		t.Fatal("open anomaly carrying resolution metadata must fail")
	}
}

func TestIsolationEventLifecycleValidation(t *testing.T) {
	now := time.Now()
	active := SpecimenIsolationEvent{
		SpecimenID: 1, TemperatureAnomalyID: 2, Reason: constants.IsolationReasonTemperature,
		ContainerID: 3, Active: true, IsolatedByID: 3, IsolatedByName: "冻存保管员", IsolatedAt: now,
	}
	if err := active.Validate(); err != nil {
		t.Fatalf("active isolation event rejected: %v", err)
	}
	released := active
	releasedBy := uint(5)
	released.Active = false
	released.ReleasedByID = &releasedBy
	released.ReleasedByName = "另一名保管员"
	released.ReleasedAt = &now
	if err := released.Validate(); err == nil {
		t.Fatal("released isolation without basis must fail")
	}
	released.ReleaseBasis = "维修完成并复核温度合格，恢复冻存"
	if err := released.Validate(); err != nil {
		t.Fatalf("released isolation with basis rejected: %v", err)
	}
}

func TestStoredSpecimenInColdStorageFlag(t *testing.T) {
	containerID := uint(2)
	stored := validSpecimen()
	stored.State = constants.SpecimenStateStored
	stored.StorageContainerID = &containerID
	stored.Position = "R01-B01-A01"
	if !stored.InColdStorage() {
		t.Fatal("located stored specimen must be reported as in cold storage")
	}
	received := validSpecimen()
	if received.InColdStorage() {
		t.Fatal("received specimen without position is not in cold storage")
	}
}
