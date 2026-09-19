package repository

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"biosample-cold-custody-tracking/backend/internal/constants"
	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/model"
)

var (
	ErrAnomalyAlreadyOpen      = errors.New("temperature anomaly is already open for the container")
	ErrAnomalyNotOpen          = errors.New("temperature anomaly is not open")
	ErrAnomalyCannotSelfClose  = errors.New("temperature anomaly cannot be closed by its reporter")
	ErrAnomalyRecoveryRequired = errors.New("recovered temperature reading is required to resolve the anomaly")
	ErrAnomalyStillOutOfRange  = errors.New("recovered temperature is still outside the container range")
	ErrSpecimenIsolated        = errors.New("specimen is isolated by an open temperature anomaly")
)

// containerAdvisoryLockID derives a stable advisory-lock id for a storage
// container. Transfer resolution uses the same function so patrol reports and
// custody moves serialize on one ordered set of locks.
func containerAdvisoryLockID(containerID uint) int64 {
	return 50520260900 + int64(containerID)
}

type AnomalyFilter struct {
	dto.PageQuery
	State              string `form:"state"`
	StorageContainerID uint   `form:"storageContainerId"`
}

type AnomalyResolutionInput struct {
	Decision        constants.TemperatureAnomalyState
	RecoveredC      *float64
	ResolutionBasis string
	ResolvedByID    uint
	ResolvedByName  string
	ResolvedAt      time.Time
}

type AnomalyRepository interface {
	List(context.Context, AnomalyFilter) ([]model.TemperatureAnomaly, int64, error)
	Find(context.Context, uint) (*model.TemperatureAnomaly, error)
	CountToday(context.Context) (int64, error)
	NextNumber(context.Context, time.Time) string
	OpenForContainer(context.Context, *gorm.DB, uint) (*model.TemperatureAnomaly, error)
	LockContainer(context.Context, *gorm.DB, uint) error
	// Report isolates every stored specimen and cancels their prepared transfers
	// atomically. Audit entries are appended through the provided callback so
	// they commit in the same transaction as the isolation changes.
	Report(ctx context.Context, anomaly *model.TemperatureAnomaly, emit func(*gorm.DB, AuditDraft) error) (*model.TemperatureAnomaly, error)
	// Close clears the anomaly and releases every isolated specimen; when any
	// precondition fails the whole transaction rolls back and state is kept.
	Close(ctx context.Context, id uint, input AnomalyResolutionInput, emit func(*gorm.DB, AuditDraft) error) (*model.TemperatureAnomaly, error)
}

// AuditDraft carries the data required to append an audit entry within an
// existing repository transaction.
type AuditDraft struct {
	Action     string
	EntityType string
	EntityID   uint
	Before     any
	After      any
}

type anomalyRepository struct{ db *gorm.DB }

func NewAnomalyRepository(db *gorm.DB) AnomalyRepository {
	return &anomalyRepository{db: db}
}

func (r *anomalyRepository) List(ctx context.Context, filter AnomalyFilter) ([]model.TemperatureAnomaly, int64, error) {
	query := filter.PageQuery.Normalize()
	db := r.db.WithContext(ctx).Model(&model.TemperatureAnomaly{})
	if state := strings.TrimSpace(filter.State); state != "" {
		db = db.Where("state = ?", state)
	}
	if filter.StorageContainerID > 0 {
		db = db.Where("storage_container_id = ?", filter.StorageContainerID)
	}
	if search := strings.TrimSpace(query.Search); search != "" {
		like := "%" + search + "%"
		db = db.Where("anomaly_no ILIKE ? OR description ILIKE ? OR reported_by_name ILIKE ?", like, like, like)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]model.TemperatureAnomaly, 0)
	err := db.Preload("StorageContainer").
		Order("reported_at DESC, id DESC").
		Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&items).Error
	return items, total, err
}

func (r *anomalyRepository) Find(ctx context.Context, id uint) (*model.TemperatureAnomaly, error) {
	var item model.TemperatureAnomaly
	err := r.db.WithContext(ctx).Preload("StorageContainer").
		Preload("IsolationEvents").First(&item, id).Error
	return &item, err
}

func (r *anomalyRepository) CountToday(ctx context.Context) (int64, error) {
	var count int64
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	err := r.db.WithContext(ctx).Model(&model.TemperatureAnomaly{}).Where("created_at >= ?", dayStart).Count(&count).Error
	return count, err
}

func (r *anomalyRepository) NextNumber(ctx context.Context, at time.Time) string {
	prefix := "TA-" + at.Format("20060102") + "-"
	var count int64
	r.db.WithContext(ctx).Model(&model.TemperatureAnomaly{}).
		Where("anomaly_no LIKE ?", prefix+"%").Count(&count)
	return prefix + padAnomalySequence(count+1)
}

func padAnomalySequence(value int64) string {
	digits := []byte("0000")
	if value > 9999 {
		digits = []byte("000000")
	}
	formatted := strconv.FormatInt(value, 10)
	copy(digits[len(digits)-len(formatted):], formatted)
	return string(digits)
}

func (r *anomalyRepository) LockContainer(ctx context.Context, tx *gorm.DB, containerID uint) error {
	return tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(?)", containerAdvisoryLockID(containerID)).Error
}

func (r *anomalyRepository) OpenForContainer(ctx context.Context, tx *gorm.DB, containerID uint) (*model.TemperatureAnomaly, error) {
	var item model.TemperatureAnomaly
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("storage_container_id = ? AND state = ?", containerID, constants.AnomalyStateOpen).
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *anomalyRepository) Report(ctx context.Context, anomaly *model.TemperatureAnomaly, emit func(*gorm.DB, AuditDraft) error) (*model.TemperatureAnomaly, error) {
	const maxNumberRetries = 6
	originalNumber := anomaly.AnomalyNo
	for attempt := 0; attempt < maxNumberRetries; attempt++ {
		if attempt > 0 {
			// Two different containers can be reported in the same second and
			// derive the same daily sequence; regenerate instead of surfacing
			// the cross-container race as a duplicate-number failure.
			anomaly.AnomalyNo = originalNumber + "-R" + strconv.Itoa(attempt)
			anomaly.ID = 0
		}
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return r.reportTx(ctx, tx, anomaly, emit)
		})
		if err == nil {
			return r.Find(ctx, anomaly.ID)
		}
		if !errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, err
		}
	}
	return nil, gorm.ErrDuplicatedKey
}

func (r *anomalyRepository) reportTx(ctx context.Context, tx *gorm.DB, anomaly *model.TemperatureAnomaly, emit func(*gorm.DB, AuditDraft) error) error {
	if err := r.LockContainer(ctx, tx, anomaly.StorageContainerID); err != nil {
		return err
	}
	var container model.StorageContainer
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&container, anomaly.StorageContainerID).Error; err != nil {
		return err
	}
	existing, err := r.OpenForContainer(ctx, tx, container.ID)
	if err != nil {
		return err
	}
	if existing != nil {
		return ErrAnomalyAlreadyOpen
	}
	if err := tx.Create(anomaly).Error; err != nil {
		return err
	}

	var specimens []model.Specimen
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("storage_container_id = ? AND state = ?", container.ID, constants.SpecimenStateStored).
		Find(&specimens).Error; err != nil {
		return err
	}
	now := anomaly.ReportedAt
	isolated := 0
	for index := range specimens {
		specimen := specimens[index]
		before := specimen
		specimen.Isolated = true
		specimen.IsolationAnomalyID = &anomaly.ID
		specimen.IsolatedAt = &now
		if err := tx.Save(&specimen).Error; err != nil {
			return err
		}
		event := &model.SpecimenIsolationEvent{
			SpecimenID:           specimen.ID,
			TemperatureAnomalyID: anomaly.ID,
			Reason:               constants.IsolationReasonTemperature,
			ContainerID:          container.ID,
			Active:               true,
			IsolatedByID:         anomaly.ReportedByID,
			IsolatedByName:       anomaly.ReportedByName,
			IsolatedAt:           now,
		}
		event.Normalize()
		if err := event.Validate(); err != nil {
			return err
		}
		if err := tx.Create(event).Error; err != nil {
			return err
		}
		if err := emit(tx, AuditDraft{
			Action: "specimen.isolated", EntityType: "Specimen", EntityID: specimen.ID,
			Before: before, After: specimen,
		}); err != nil {
			return err
		}
		isolated++
	}
	anomaly.IsolatedCount = isolated

	// Prepared transfers touching the quarantined container can never be
	// accepted; cancel them in the same lock window so no prepared ticket is
	// stranded waiting for a specimen that is already isolated.
	storedSpecimenIDs := tx.Model(&model.Specimen{}).Select("id").Where("storage_container_id = ?", container.ID)
	var prepared []model.CustodyTransfer
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("state = ? AND (specimen_id IN (?) OR to_container_id = ?)",
			constants.TransferStatePrepared, storedSpecimenIDs, container.ID,
		).Find(&prepared).Error; err != nil {
		return err
	}
	for index := range prepared {
		transfer := prepared[index]
		before := transfer
		transfer.State = constants.TransferStateCancelled
		resolver := anomaly.ReportedByID
		transfer.AcceptedByID = &resolver
		transfer.AcceptedByName = anomaly.ReportedByName
		transfer.ResolvedAt = &now
		transfer.Reason = strings.TrimSpace(transfer.Reason + "\n冷链异常隔离自动取消: " + anomaly.AnomalyNo)
		transfer.Normalize()
		if err := transfer.Validate(); err != nil {
			return err
		}
		if err := tx.Save(&transfer).Error; err != nil {
			return err
		}
		if err := emit(tx, AuditDraft{
			Action: "custody_transfer.cancelled", EntityType: "CustodyTransfer", EntityID: transfer.ID,
			Before: before, After: transfer,
		}); err != nil {
			return err
		}
	}

	anomaly.Normalize()
	if err := anomaly.Validate(); err != nil {
		return err
	}
	if err := tx.Save(anomaly).Error; err != nil {
		return err
	}
	return emit(tx, AuditDraft{
		Action: "temperature_anomaly.reported", EntityType: "TemperatureAnomaly", EntityID: anomaly.ID,
		Before: nil, After: anomaly,
	})
}

func (r *anomalyRepository) Close(ctx context.Context, id uint, input AnomalyResolutionInput, emit func(*gorm.DB, AuditDraft) error) (*model.TemperatureAnomaly, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.closeTx(ctx, tx, id, input, emit)
	})
	if err != nil {
		return nil, err
	}
	return r.Find(ctx, id)
}

func (r *anomalyRepository) closeTx(ctx context.Context, tx *gorm.DB, id uint, input AnomalyResolutionInput, emit func(*gorm.DB, AuditDraft) error) error {
	var anomaly model.TemperatureAnomaly
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&anomaly, id).Error; err != nil {
		return err
	}
	if !anomaly.IsOpen() {
		return ErrAnomalyNotOpen
	}
	if anomaly.ReportedByID == input.ResolvedByID {
		return ErrAnomalyCannotSelfClose
	}
	if err := r.LockContainer(ctx, tx, anomaly.StorageContainerID); err != nil {
		return err
	}
	var container model.StorageContainer
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&container, anomaly.StorageContainerID).Error; err != nil {
		return err
	}
	if input.Decision == constants.AnomalyStateResolved {
		if input.RecoveredC == nil {
			return ErrAnomalyRecoveryRequired
		}
		if !container.AcceptsTemperature(*input.RecoveredC) {
			return ErrAnomalyStillOutOfRange
		}
	}
	before := anomaly

	var events []model.SpecimenIsolationEvent
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("temperature_anomaly_id = ? AND active = ?", anomaly.ID, true).
		Find(&events).Error; err != nil {
		return err
	}
	for index := range events {
		event := events[index]
		var specimen model.Specimen
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&specimen, event.SpecimenID).Error; err != nil {
			return err
		}
		specimenBefore := specimen
		if specimen.IsolationAnomalyID != nil && *specimen.IsolationAnomalyID == anomaly.ID {
			specimen.Isolated = false
			specimen.IsolationAnomalyID = nil
			specimen.IsolatedAt = nil
			if err := tx.Save(&specimen).Error; err != nil {
				return err
			}
		}
		event.Active = false
		event.ReleasedByID = &input.ResolvedByID
		event.ReleasedByName = strings.TrimSpace(input.ResolvedByName)
		event.ReleasedAt = &input.ResolvedAt
		event.ReleaseBasis = strings.TrimSpace(input.ResolutionBasis)
		event.Outcome = string(input.Decision)
		event.Normalize()
		if err := event.Validate(); err != nil {
			return err
		}
		if err := tx.Save(&event).Error; err != nil {
			return err
		}
		// Both "recovered" and "false alarm" outcomes lift the quarantine, so
		// every specimen gets an isolation-release audit entry.
		if err := emit(tx, AuditDraft{
			Action: "specimen.isolation_released", EntityType: "Specimen", EntityID: specimen.ID,
			Before: specimenBefore, After: specimen,
		}); err != nil {
			return err
		}
	}

	anomaly.State = input.Decision
	anomaly.ResolvedByID = &input.ResolvedByID
	anomaly.ResolvedByName = strings.TrimSpace(input.ResolvedByName)
	anomaly.ResolvedAt = &input.ResolvedAt
	anomaly.RecoveredC = input.RecoveredC
	anomaly.ResolutionBasis = strings.TrimSpace(input.ResolutionBasis)
	anomaly.Normalize()
	if err := anomaly.Validate(); err != nil {
		return err
	}
	if err := tx.Save(&anomaly).Error; err != nil {
		return err
	}
	action := "temperature_anomaly.resolved"
	if input.Decision == constants.AnomalyStateInvalid {
		action = "temperature_anomaly.invalidated"
	}
	return emit(tx, AuditDraft{
		Action: action, EntityType: "TemperatureAnomaly", EntityID: anomaly.ID,
		Before: before, After: anomaly,
	})
}
