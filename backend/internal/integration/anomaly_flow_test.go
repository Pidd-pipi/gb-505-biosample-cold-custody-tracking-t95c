//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"biosample-cold-custody-tracking/backend/internal/constants"
	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/model"
	"biosample-cold-custody-tracking/backend/internal/repository"
	"biosample-cold-custody-tracking/backend/internal/service"
	"biosample-cold-custody-tracking/backend/internal/util"
)

func dsn() string {
	if value := os.Getenv("TEST_DATABASE_URL"); value != "" {
		return value
	}
	return "postgres://postgres@localhost:5432/postgres?sslmode=disable"
}

type fixtures struct {
	db           *gorm.DB
	container    model.StorageContainer
	specimens    []model.Specimen
	anomalyRepo  repository.AnomalyRepository
	storageRepo  repository.StorageRepository
	specimenRepo repository.SpecimenRepository
	transferRepo repository.TransferRepository
	protocolRepo repository.ProtocolRepository
}

func setup(t *testing.T) *fixtures {
	t.Helper()
	db, err := util.OpenDatabase(dsn())
	if err != nil {
		t.Skipf("integration database unavailable: %v", err)
	}
	if err := util.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cleanup(t, db)

	f := &fixtures{
		db:           db,
		anomalyRepo:  repository.NewAnomalyRepository(db),
		storageRepo:  repository.NewStorageRepository(db),
		specimenRepo: repository.NewSpecimenRepository(db),
		transferRepo: repository.NewTransferRepository(db),
		protocolRepo: repository.NewProtocolRepository(db),
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	container := model.StorageContainer{
		Code: "FZ-IT-" + suffix, Name: "集成测试柜", ContainerType: "freezer",
		TemperatureZone: "minus80", Location: "IT 区", Capacity: 100, Occupied: 0,
		Status: "available", Active: true,
	}
	if err := db.Create(&container).Error; err != nil {
		t.Fatalf("create container: %v", err)
	}
	f.container = container

	for i := 0; i < 3; i++ {
		pos := fmt.Sprintf("R%02d-BX01-A%02d", i, i)
		specimen := model.Specimen{
			AccessionNo:        fmt.Sprintf("BIO-IT-%s-%d", suffix, i),
			SampleType:         "血浆",
			SubjectCode:        fmt.Sprintf("SUBJ-IT-%s-%d", suffix, i),
			ProtocolCode:       "PROTO-IT-001",
			State:              constants.SpecimenStateStored,
			StorageContainerID: &container.ID,
			Position:           pos,
			VolumeML:           1.0,
			CurrentCustodian:   "冻存保管员",
			ReceivedAt:         time.Now().Add(-2 * time.Hour),
		}
		if err := db.Create(&specimen).Error; err != nil {
			t.Fatalf("create specimen: %v", err)
		}
		f.specimens = append(f.specimens, specimen)
	}
	return f
}

func cleanup(t *testing.T, db *gorm.DB) {
	t.Helper()
	_ = db.Exec("DELETE FROM specimen_quarantines").Error
	_ = db.Exec("DELETE FROM custody_transfers").Error
	_ = db.Exec("DELETE FROM protocol_reviews").Error
	_ = db.Exec("DELETE FROM specimens").Error
	_ = db.Exec("DELETE FROM temperature_anomalies").Error
	_ = db.Exec("DELETE FROM storage_containers WHERE code LIKE 'FZ-IT-%'").Error
}

type recordingAudit struct{ service.AuditService }

func (recordingAudit) Record(context.Context, service.Actor, string, string, uint, any, any) error {
	return nil
}
func (recordingAudit) List(context.Context, repository.AuditFilter) (dto.PageResult[model.AuditLog], error) {
	return dto.PageResult[model.AuditLog]{}, nil
}
func (recordingAudit) Verify(context.Context) error { return nil }

func TestAnomalyLifecycleIsolatesAndReleases(t *testing.T) {
	f := setup(t)
	defer cleanup(t, f.db)
	ctx := context.Background()
	svc := service.NewAnomalyService(f.anomalyRepo, f.storageRepo, recordingAudit{})

	reporter := service.Actor{ID: 101, Name: "上报员甲", RequestID: "it-report"}
	anomaly, err := svc.Report(ctx, reporter, dto.CreateAnomalyRequest{
		StorageContainerID: f.container.ID, TemperatureC: -50.0, Description: "温度回升超限",
	})
	if err != nil {
		t.Fatalf("report anomaly: %v", err)
	}
	if anomaly.Status != constants.AnomalyStateOpen || anomaly.AffectedCount != 3 {
		t.Fatalf("unexpected anomaly status=%s affected=%d", anomaly.Status, anomaly.AffectedCount)
	}

	// 容器内全部已冻存样本必须被隔离。
	for _, sample := range f.specimens {
		got, err := f.specimenRepo.Find(ctx, sample.ID)
		if err != nil {
			t.Fatalf("find specimen: %v", err)
		}
		if !got.Isolated() || *got.QuarantineAnomalyID != anomaly.ID {
			t.Fatalf("specimen %d must be isolated by anomaly %d", sample.ID, anomaly.ID)
		}
		if len(got.QuarantineHistory) != 1 || got.QuarantineHistory[0].Action != constants.QuarantineActionIsolated {
			t.Fatalf("specimen %d must have one isolate history entry", sample.ID)
		}
	}

	// 同一容器重复建单必须冲突。
	if _, err := svc.Report(ctx, reporter, dto.CreateAnomalyRequest{
		StorageContainerID: f.container.ID, TemperatureC: -49.0,
	}); err == nil {
		t.Fatal("second open anomaly for same container must be rejected")
	}

	// 温度在范围内不能建单（另一容器无异常时）。
	if _, err := svc.Report(ctx, reporter, dto.CreateAnomalyRequest{
		StorageContainerID: f.container.ID, TemperatureC: -80.0,
	}); err == nil {
		t.Fatal("in-range temperature must not create an anomaly")
	}

	// 隔离样本不能发起交接（服务层）。
	transferSvc := service.NewTransferService(f.transferRepo, f.specimenRepo, recordingAudit{})
	targetSpecimen := f.specimens[0]
	locked, _ := f.specimenRepo.Find(ctx, targetSpecimen.ID)
	_, err = transferSvc.Create(ctx, reporter, dto.CreateTransferRequest{
		SpecimenID: targetSpecimen.ID, TransferNo: "CT-IT-BLOCKED",
		FromCustodian: locked.CurrentCustodian, ToCustodian: "接收人乙",
		FromLocation: locked.LocationLabel(), ToLocation: "IT 区 2",
	})
	if err == nil {
		t.Fatal("quarantined specimen must not start a transfer")
	}

	// 建单人不能解除。
	other := service.Actor{ID: 102, Name: "复核员乙", RequestID: "it-release-self"}
	if _, err := svc.Release(ctx, reporter, anomaly.ID, dto.ReleaseAnomalyRequest{ReleaseBasis: "依据"}); err == nil {
		t.Fatal("reporter must not release their own anomaly")
	}

	// 依据过短拒绝。
	if _, err := svc.Release(ctx, other, anomaly.ID, dto.ReleaseAnomalyRequest{ReleaseBasis: "ab"}); err == nil {
		t.Fatal("release basis shorter than 3 chars must be rejected")
	}

	// 非建单人填写依据解除。
	released, err := svc.Release(ctx, other, anomaly.ID, dto.ReleaseAnomalyRequest{
		ReleaseBasis: "连续 2 小时回落至 -80°C，设备维修完成",
	})
	if err != nil {
		t.Fatalf("release anomaly: %v", err)
	}
	if released.Status != constants.AnomalyStateReleased || released.ReleasedByID == nil || *released.ReleasedByID != 102 {
		t.Fatal("anomaly must be released by the other operator")
	}
	for _, sample := range f.specimens {
		got, _ := f.specimenRepo.Find(ctx, sample.ID)
		if got.Isolated() {
			t.Fatalf("specimen %d must be un-isolated after release", sample.ID)
		}
		if len(got.QuarantineHistory) != 2 {
			t.Fatalf("specimen %d must keep isolate+release history, got %d", sample.ID, len(got.QuarantineHistory))
		}
	}

	// 重复解除必须失败。
	if _, err := svc.Release(ctx, other, anomaly.ID, dto.ReleaseAnomalyRequest{ReleaseBasis: "再次解除依据"}); err == nil {
		t.Fatal("released anomaly must not be released again")
	}
}

func TestConcurrentReportsCreateOnlyOneAnomaly(t *testing.T) {
	f := setup(t)
	defer cleanup(t, f.db)
	ctx := context.Background()
	svc := service.NewAnomalyService(f.anomalyRepo, f.storageRepo, recordingAudit{})

	const workers = 8
	var wg sync.WaitGroup
	successes := make(chan uint, workers)
	errs := make(chan error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			actor := service.Actor{ID: uint(200 + idx), Name: fmt.Sprintf("巡检员%d", idx), RequestID: fmt.Sprintf("it-conc-%d", idx)}
			anomaly, err := svc.Report(ctx, actor, dto.CreateAnomalyRequest{
				StorageContainerID: f.container.ID, TemperatureC: -48.0, Description: "并发巡检",
			})
			if err == nil {
				successes <- anomaly.ID
			} else {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(successes)
	close(errs)

	var ids []uint
	for id := range successes {
		ids = append(ids, id)
	}
	if len(ids) != 1 {
		t.Fatalf("exactly one anomaly must be created under concurrency, got %d (errors=%d)", len(ids), len(errs))
	}
	var openCount int64
	if err := f.db.Model(&model.TemperatureAnomaly{}).
		Where("storage_container_id = ? AND status = ?", f.container.ID, constants.AnomalyStateOpen).
		Count(&openCount).Error; err != nil {
		t.Fatal(err)
	}
	if openCount != 1 {
		t.Fatalf("container must have exactly one open anomaly, got %d", openCount)
	}
	// 全部已冻存样本都被那一张单隔离，没有漏隔离。
	var isolatedCount int64
	if err := f.db.Model(&model.Specimen{}).
		Where("storage_container_id = ? AND quarantine_anomaly_id = ?", f.container.ID, ids[0]).
		Count(&isolatedCount).Error; err != nil {
		t.Fatal(err)
	}
	if isolatedCount != int64(len(f.specimens)) {
		t.Fatalf("all stored specimens must be isolated, got %d of %d", isolatedCount, len(f.specimens))
	}
}

func TestTransferIntoAnomalousContainerAutoIsolates(t *testing.T) {
	f := setup(t)
	defer cleanup(t, f.db)
	ctx := context.Background()

	// 目标容器（已有一张未结异常，0 个已冻存样本）。
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%100000000)
	target := model.StorageContainer{
		Code: "FZ-IT-DST-" + suffix, Name: "异常目标柜", ContainerType: "freezer",
		TemperatureZone: "minus80", Location: "IT 目标区", Capacity: 100, Status: "available", Active: true,
	}
	if err := f.db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	anomalySvc := service.NewAnomalyService(f.anomalyRepo, f.storageRepo, recordingAudit{})
	openAnomaly, err := anomalySvc.Report(ctx, service.Actor{ID: 301, Name: "上报员", RequestID: "it-dst"},
		dto.CreateAnomalyRequest{StorageContainerID: target.ID, TemperatureC: -55})
	if err != nil {
		t.Fatalf("report target anomaly: %v", err)
	}

	// 源样本容器正常，样本未隔离，发起交接。
	source := f.specimens[0]
	current, _ := f.specimenRepo.Find(ctx, source.ID)
	transfer := &model.CustodyTransfer{
		SpecimenID: source.ID, TransferNo: "CT-IT-AUTO-" + suffix,
		FromCustodian: current.CurrentCustodian, ToCustodian: "接收保管员",
		FromLocation: current.LocationLabel(), ToLocation: "IT 目标区",
		ToPosition: "D01-BX01-A01",
		State:      constants.TransferStatePrepared, PreparedByID: 302, PreparedByName: "移交员",
		PreparedAt: time.Now(),
	}
	if err := f.transferRepo.Create(ctx, transfer); err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	temp := -80.0
	_, _, _, _, err = f.transferRepo.Resolve(ctx, transfer.ID, repository.TransferResolution{
		State: constants.TransferStateAccepted, ToContainerID: &target.ID,
		ToPosition: "D01-BX01-A01", TemperatureC: &temp,
		ResolvedByID: 303, ResolvedByName: "接收保管员", ResolvedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("resolve transfer into anomalous container: %v", err)
	}
	moved, _ := f.specimenRepo.Find(ctx, source.ID)
	if !moved.Isolated() || *moved.QuarantineAnomalyID != openAnomaly.ID {
		t.Fatal("specimen moved into an open-anomaly container must be auto-isolated")
	}
	if moved.State != constants.SpecimenStateStored || moved.StorageContainerID == nil || *moved.StorageContainerID != target.ID {
		t.Fatal("specimen must be stored at the target container")
	}
}

func TestReportRacesWithTransfersNoMissingIsolation(t *testing.T) {
	db, err := util.OpenDatabase(dsn())
	if err != nil {
		t.Skipf("integration database unavailable: %v", err)
	}
	if err := util.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 每个样本一个独立源容器，全部并发调入同一目标容器，同时对目标容器上报超限。
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%100000000)
	target := model.StorageContainer{
		Code: "FZ-IT-RACE-DST-" + suffix, Name: "竞争目标柜", ContainerType: "freezer",
		TemperatureZone: "minus80", Location: "IT 竞争区", Capacity: 1000, Status: "available", Active: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	const specimenCount = 6
	type moveSpec struct {
		specimen  model.Specimen
		container model.StorageContainer
		transfer  *model.CustodyTransfer
	}
	specimenRepo := repository.NewSpecimenRepository(db)
	moves := make([]moveSpec, 0, specimenCount)
	for i := 0; i < specimenCount; i++ {
		src := model.StorageContainer{
			Code: fmt.Sprintf("FZ-IT-RACE-SRC-%s-%d", suffix, i), Name: "竞争源柜", ContainerType: "freezer",
			TemperatureZone: "minus80", Location: "IT 竞争源区", Capacity: 100, Occupied: 1, Status: "available", Active: true,
		}
		if err := db.Create(&src).Error; err != nil {
			t.Fatal(err)
		}
		sample := model.Specimen{
			AccessionNo: fmt.Sprintf("BIO-IT-RACE-%s-%d", suffix, i), SampleType: "血浆",
			SubjectCode: fmt.Sprintf("SUBJ-RACE-%s-%d", suffix, i), ProtocolCode: "PROTO-IT-RACE",
			State: constants.SpecimenStateStored, StorageContainerID: &src.ID,
			Position: fmt.Sprintf("S%02d-BX01-A01", i), VolumeML: 1.0,
			CurrentCustodian: fmt.Sprintf("移交员%d", i), ReceivedAt: time.Now().Add(-time.Hour),
		}
		if err := db.Create(&sample).Error; err != nil {
			t.Fatal(err)
		}
		// 重新完整加载（预载容器），确保 FromLocation 与受理事务中的 LocationLabel 完全一致。
		loaded, err := specimenRepo.Find(context.Background(), sample.ID)
		if err != nil {
			t.Fatal(err)
		}
		sample = *loaded
		transfer := &model.CustodyTransfer{
			SpecimenID: sample.ID, TransferNo: fmt.Sprintf("CT-IT-RACE-%s-%d", suffix, i),
			FromCustodian: sample.CurrentCustodian, ToCustodian: "接收保管员",
			FromLocation: sample.LocationLabel(), ToLocation: "IT 竞争区",
			State: constants.TransferStatePrepared, PreparedByID: 500 + uint(i), PreparedByName: sample.CurrentCustodian,
			PreparedAt: time.Now(),
		}
		if err := db.Create(transfer).Error; err != nil {
			t.Fatal(err)
		}
		moves = append(moves, moveSpec{specimen: sample, container: src, transfer: transfer})
	}
	defer func() {
		_ = db.Exec("DELETE FROM specimen_quarantines").Error
		_ = db.Exec("DELETE FROM custody_transfers WHERE transfer_no LIKE 'CT-IT-RACE-%'").Error
		_ = db.Exec("DELETE FROM specimens WHERE accession_no LIKE 'BIO-IT-RACE-%'").Error
		_ = db.Exec("DELETE FROM temperature_anomalies WHERE storage_container_id = ?", target.ID).Error
		_ = db.Exec("DELETE FROM storage_containers WHERE code LIKE 'FZ-IT-RACE-%'").Error
	}()

	transferRepo := repository.NewTransferRepository(db)
	anomalyRepo := repository.NewAnomalyRepository(db)
	storageRepo := repository.NewStorageRepository(db)
	anomalySvc := service.NewAnomalyService(anomalyRepo, storageRepo, recordingAudit{})

	var wg sync.WaitGroup
	start := make(chan struct{})
	resolveErrs := make(chan error, specimenCount)
	for i, move := range moves {
		wg.Add(1)
		go func(idx int, m moveSpec) {
			defer wg.Done()
			<-start
			temp := -79.0
			_, _, _, _, err := transferRepo.Resolve(context.Background(), m.transfer.ID, repository.TransferResolution{
				State: constants.TransferStateAccepted, ToContainerID: &target.ID,
				ToPosition: fmt.Sprintf("T%02d-BX01-A01", idx), TemperatureC: &temp,
				ResolvedByID: 600 + uint(idx), ResolvedByName: "接收保管员", ResolvedAt: time.Now(),
			})
			if err != nil {
				resolveErrs <- err
			}
		}(i, move)
	}
	// 多个并发超限读数 + 多笔调拨，同一目标容器。
	reportErrs := make(chan error, 4)
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, err := anomalySvc.Report(context.Background(), service.Actor{ID: 700 + uint(idx), Name: fmt.Sprintf("巡检员%d", idx), RequestID: fmt.Sprintf("it-race-report-%d", idx)},
				dto.CreateAnomalyRequest{StorageContainerID: target.ID, TemperatureC: -48.0, Description: "竞争超限"})
			if err != nil {
				reportErrs <- err
			}
		}(r)
	}
	close(start)
	wg.Wait()
	close(resolveErrs)
	close(reportErrs)
	for err := range resolveErrs {
		t.Errorf("transfer resolution failed during race: %v", err)
	}
	// 并发上报中除一张外都应收到冲突错误；目标容器 open 异常必须恰为一张。
	var openAnomalies []model.TemperatureAnomaly
	if err := db.Where("storage_container_id = ? AND status = ?", target.ID, constants.AnomalyStateOpen).Find(&openAnomalies).Error; err != nil {
		t.Fatal(err)
	}
	if len(openAnomalies) != 1 {
		t.Fatalf("target container must have exactly one open anomaly, got %d", len(openAnomalies))
	}

	// 全部调入样本最终都必须在目标容器内且处于隔离，不允许漏隔离。
	for _, move := range moves {
		var sample model.Specimen
		if err := db.First(&sample, move.specimen.ID).Error; err != nil {
			t.Fatal(err)
		}
		if sample.StorageContainerID == nil || *sample.StorageContainerID != target.ID {
			t.Fatalf("specimen %d must end in target container", sample.ID)
		}
		if !sample.Isolated() || *sample.QuarantineAnomalyID != openAnomalies[0].ID {
			t.Fatalf("specimen %d must be isolated by the sole anomaly after race", sample.ID)
		}
	}
}

func TestQuarantinedSpecimenRejectsApprovalButAllowsHold(t *testing.T) {
	f := setup(t)
	defer cleanup(t, f.db)
	ctx := context.Background()
	anomalySvc := service.NewAnomalyService(f.anomalyRepo, f.storageRepo, recordingAudit{})
	if _, err := anomalySvc.Report(ctx, service.Actor{ID: 401, Name: "上报员", RequestID: "it-rev"},
		dto.CreateAnomalyRequest{StorageContainerID: f.container.ID, TemperatureC: -50}); err != nil {
		t.Fatalf("report: %v", err)
	}
	sample := f.specimens[0]

	makeReview := func(decision constants.ReviewDecision) *model.ProtocolReview {
		return &model.ProtocolReview{
			SpecimenID: sample.ID, ProtocolCode: sample.ProtocolCode, Decision: decision,
			ReviewerID: 402, ReviewerName: "复核员", ReviewedAt: time.Now(),
			ConsentVerified: true, ScopeVerified: true, Notes: "说明备注",
		}
	}
	if _, _, _, err := f.protocolRepo.Create(ctx, makeReview(constants.DecisionApproved)); err == nil {
		t.Fatal("approved review for quarantined specimen must be rejected")
	}
	hold := makeReview(constants.DecisionHold)
	created, after, _, err := f.protocolRepo.Create(ctx, hold)
	if err != nil {
		t.Fatalf("hold review must succeed while quarantined: %v", err)
	}
	if created.Decision != constants.DecisionHold || after.Isolated() != true {
		t.Fatal("hold review must be recorded and specimen must remain isolated")
	}
}
