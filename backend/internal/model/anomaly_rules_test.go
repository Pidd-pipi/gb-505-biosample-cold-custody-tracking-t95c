package model

import (
	"testing"
	"time"

	"biosample-cold-custody-tracking/backend/internal/constants"
)

func validAnomaly() TemperatureAnomaly {
	return TemperatureAnomaly{
		AnomalyNo:          "TA-20260919-001",
		StorageContainerID: 3,
		TemperatureC:       -52.4,
		ZoneLowerC:         -90,
		ZoneUpperC:         -65,
		InspectedAt:        time.Now().Add(-time.Minute),
		Status:             constants.AnomalyStateOpen,
		Description:        "巡检发现温度回升",
		ReportedByID:       7,
		ReportedByName:     "冻存保管员",
	}
}

func TestOpenAnomalyValidation(t *testing.T) {
	anomaly := validAnomaly()
	if err := anomaly.Validate(); err != nil {
		t.Fatalf("valid open anomaly rejected: %v", err)
	}
	if !anomaly.Open() {
		t.Fatal("new anomaly must be open")
	}
	anomaly.Status = constants.AnomalyStateReleased
	if err := anomaly.Validate(); err == nil {
		t.Fatal("released anomaly without resolver, basis and time must fail")
	}
	resolver := uint(9)
	releasedAt := time.Now()
	anomaly.ReleasedByID = &resolver
	anomaly.ReleasedByName = "另一名保管员"
	anomaly.ReleaseBasis = "连续两小时温度恢复至 -80°C"
	anomaly.ReleasedAt = &releasedAt
	if err := anomaly.Validate(); err != nil {
		t.Fatalf("valid released anomaly rejected: %v", err)
	}
}

func TestAnomalyRejectsOutOfBandTemperature(t *testing.T) {
	anomaly := validAnomaly()
	anomaly.TemperatureC = 100
	if err := anomaly.Validate(); err == nil {
		t.Fatal("temperature above range must be rejected")
	}
	anomaly = validAnomaly()
	anomaly.ZoneLowerC = anomaly.ZoneUpperC
	if err := anomaly.Validate(); err == nil {
		t.Fatal("invalid zone range must be rejected")
	}
}

func TestSpecimenIsolationFlag(t *testing.T) {
	base := validSpecimen()
	base.State = constants.SpecimenStateStored
	containerID := uint(2)
	base.StorageContainerID = &containerID
	base.Position = "R01-BX01-A01"
	if base.Isolated() {
		t.Fatal("freshly stored specimen must not be isolated")
	}
	anomalyID := uint(11)
	base.QuarantineAnomalyID = &anomalyID
	if !base.Isolated() {
		t.Fatal("specimen linked to an open anomaly must report isolated")
	}
}

func TestContainerOpenAnomalySummary(t *testing.T) {
	container := StorageContainer{Code: "FZ-80-01", Name: "柜", ContainerType: "freezer", TemperatureZone: "minus80", Location: "B", Capacity: 10, Status: "available", Active: true}
	if container.HasOpenAnomaly() {
		t.Fatal("container without anomaly summary must be clear")
	}
	anomalyID := uint(5)
	container.OpenAnomalyID = &anomalyID
	container.AnomalyStatus = "open"
	container.IsolatedSpecimenCount = 4
	if !container.HasOpenAnomaly() {
		t.Fatal("container with open anomaly id must report an active anomaly")
	}
}

func TestQuarantineEventValidation(t *testing.T) {
	event := SpecimenQuarantine{
		SpecimenID: 1, AnomalyID: 2, StorageContainerID: 3,
		Action: constants.QuarantineActionIsolated, OperatorID: 7, OperatorName: "保管员",
		Reason: "超限隔离",
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("valid isolate event rejected: %v", err)
	}
	event.Action = "tampered"
	if err := event.Validate(); err == nil {
		t.Fatal("unknown quarantine action must be rejected")
	}
}
