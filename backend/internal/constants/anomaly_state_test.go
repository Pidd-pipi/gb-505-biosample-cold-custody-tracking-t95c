package constants

import "testing"

func TestAnomalyStateLifecycle(t *testing.T) {
	if !AnomalyStateOpen.Valid() || !AnomalyStateReleased.Valid() {
		t.Fatal("open and released must be valid anomaly states")
	}
	if !AnomalyStateOpen.Open() || AnomalyStateReleased.Open() {
		t.Fatal("only open anomalies are actionable")
	}
	if err := AnomalyStateOpen.CanReleaseTo(AnomalyStateReleased); err != nil {
		t.Fatalf("open anomaly must release to released: %v", err)
	}
	if err := AnomalyStateReleased.CanReleaseTo(AnomalyStateReleased); err == nil {
		t.Fatal("released anomaly must not be releasable again")
	}
	if err := AnomalyStateOpen.CanReleaseTo(AnomalyStateOpen); err == nil {
		t.Fatal("anomaly cannot stay open when resolving")
	}
}

func TestQuarantineActions(t *testing.T) {
	if !QuarantineActionIsolated.Valid() || !QuarantineActionReleased.Valid() {
		t.Fatal("isolate and release must be valid quarantine actions")
	}
	if QuarantineAction("forged").Valid() {
		t.Fatal("unknown quarantine action must be invalid")
	}
}

func TestCustodianHoldsAnomalyPermission(t *testing.T) {
	if !RoleCustodian.Can("anomaly:manage") {
		t.Fatal("custodian must manage temperature anomalies")
	}
	if !RoleAdmin.Can("anomaly:manage") {
		t.Fatal("admin must manage temperature anomalies")
	}
	if RoleReceiver.Can("anomaly:manage") || RoleReviewer.Can("anomaly:manage") || RoleAuditor.Can("anomaly:manage") {
		t.Fatal("receiver, reviewer and auditor must not manage anomalies")
	}
}
