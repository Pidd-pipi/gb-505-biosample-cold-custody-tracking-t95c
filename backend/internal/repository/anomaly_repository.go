package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"biosample-cold-custody-tracking/backend/internal/constants"
	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/model"
)

var (
	// ErrOpenAnomalyExists 表示同一冻存容器已存在未结异常单。
	ErrOpenAnomalyExists = errors.New("an open temperature anomaly already exists for the container")
	// ErrTemperatureWithinRange 表示巡检读数仍在容器温区范围内，无需建单。
	ErrTemperatureWithinRange = errors.New("recorded temperature is still within the container zone range")
	// ErrAnomalyNotOpen 表示异常单已解除，不能重复解除。
	ErrAnomalyNotOpen = errors.New("temperature anomaly is not open")
	// ErrAnomalyNumberTaken 表示异常单号冲突（可换号重试）。
	ErrAnomalyNumberTaken = errors.New("temperature anomaly number already exists")
)

type AnomalyFilter struct {
	dto.PageQuery
	Status             string `form:"status"`
	StorageContainerID uint   `form:"storageContainerId"`
}

// ReportAnomalyInput 为事务内建单所需的全部参数。
type ReportAnomalyInput struct {
	AnomalyNo    string
	ContainerID  uint
	TemperatureC float64
	InspectedAt  time.Time
	Description  string
	ReporterID   uint
	ReporterName string
}

// AnomalyReportResult 返回建单结果及被隔离样本的前后快照，供审计使用。
type AnomalyReportResult struct {
	Anomaly        *model.TemperatureAnomaly
	BeforeIsolated []model.Specimen
	AfterIsolated  []model.Specimen
}

// AnomalyReleaseResult 返回解除结果及恢复样本的前后快照，供审计使用。
type AnomalyReleaseResult struct {
	Anomaly        *model.TemperatureAnomaly
	BeforeReleased []model.Specimen
	AfterReleased  []model.Specimen
}

// AnomalyContainerSummary 为冻存页提供未结异常与涉及样本数。
type AnomalyContainerSummary struct {
	AnomalyID     uint  `gorm:"column:anomaly_id"`
	ContainerID   uint  `gorm:"column:container_id"`
	IsolatedCount int64 `gorm:"column:isolated_count"`
}

type AnomalyRepository interface {
	List(context.Context, AnomalyFilter) ([]model.TemperatureAnomaly, int64, error)
	Find(context.Context, uint) (*model.TemperatureAnomaly, error)
	Report(context.Context, ReportAnomalyInput) (*AnomalyReportResult, error)
	Release(context.Context, uint, uint, string, string) (*AnomalyReleaseResult, error)
	OpenSummaries(context.Context, []uint) (map[uint]AnomalyContainerSummary, error)
}

type anomalyRepository struct{ db *gorm.DB }

func NewAnomalyRepository(db *gorm.DB) AnomalyRepository {
	return &anomalyRepository{db: db}
}

func (r *anomalyRepository) List(ctx context.Context, filter AnomalyFilter) ([]model.TemperatureAnomaly, int64, error) {
	query := filter.PageQuery.Normalize()
	db := r.db.WithContext(ctx).Model(&model.TemperatureAnomaly{})
	if status := strings.TrimSpace(filter.Status); status != "" {
		db = db.Where("status = ?", status)
	}
	if filter.StorageContainerID > 0 {
		db = db.Where("storage_container_id = ?", filter.StorageContainerID)
	}
	if search := strings.TrimSpace(query.Search); search != "" {
		like := "%" + search + "%"
		db = db.Where("anomaly_no ILIKE ? OR description ILIKE ? OR reported_by_name ILIKE ? OR released_by_name ILIKE ?", like, like, like, like)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]model.TemperatureAnomaly, 0)
	err := db.Preload("StorageContainer").
		Order("inspected_at DESC, id DESC").
		Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&items).Error
	return items, total, err
}

func (r *anomalyRepository) Find(ctx context.Context, id uint) (*model.TemperatureAnomaly, error) {
	var item model.TemperatureAnomaly
	err := r.db.WithContext(ctx).
		Preload("StorageContainer").
		Preload("QuarantineEvents", func(tx *gorm.DB) *gorm.DB { return tx.Order("created_at DESC, id DESC") }).
		Preload("QuarantineEvents.Specimen").
		Preload("QuarantineEvents.StorageContainer").
		First(&item, id).Error
	return &item, err
}

// isUniqueIndexViolation 依赖 gorm 的错误翻译判断唯一索引冲突。
func isUniqueIndexViolation(err error) bool {
	return err != nil && errors.Is(err, gorm.ErrDuplicatedKey)
}

func (r *anomalyRepository) Report(ctx context.Context, input ReportAnomalyInput) (*AnomalyReportResult, error) {
	result := &AnomalyReportResult{}
	var anomalyID uint
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 容器行锁是与交接受理并发的串行点：必须先锁容器，再锁异常与样本。
		var container model.StorageContainer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&container, input.ContainerID).Error; err != nil {
			return err
		}
		lower, upper := container.TemperatureRange()
		if input.TemperatureC >= lower && input.TemperatureC <= upper {
			return ErrTemperatureWithinRange
		}
		var openCount int64
		if err := tx.Model(&model.TemperatureAnomaly{}).
			Where("storage_container_id = ? AND status = ?", container.ID, constants.AnomalyStateOpen).
			Count(&openCount).Error; err != nil {
			return err
		}
		if openCount > 0 {
			return ErrOpenAnomalyExists
		}

		anomaly := &model.TemperatureAnomaly{
			AnomalyNo:          strings.ToUpper(strings.TrimSpace(input.AnomalyNo)),
			StorageContainerID: container.ID,
			TemperatureC:       input.TemperatureC,
			ZoneLowerC:         lower,
			ZoneUpperC:         upper,
			InspectedAt:        input.InspectedAt,
			Description:        strings.TrimSpace(input.Description),
			Status:             constants.AnomalyStateOpen,
			ReportedByID:       input.ReporterID,
			ReportedByName:     strings.TrimSpace(input.ReporterName),
		}
		anomaly.Normalize()
		if err := anomaly.Validate(); err != nil {
			return err
		}
		// 持容器锁后预查单号，避免唯一冲突后事务进入 aborted 状态无法继续。
		var numberTaken int64
		if err := tx.Model(&model.TemperatureAnomaly{}).Where("anomaly_no = ?", anomaly.AnomalyNo).Count(&numberTaken).Error; err != nil {
			return err
		}
		if numberTaken > 0 {
			return ErrAnomalyNumberTaken
		}
		if err := tx.Create(anomaly).Error; err != nil {
			// 容器锁与 openCount 已兜底；走到这里只可能是并发下部分唯一索引竞争。
			if isUniqueIndexViolation(err) {
				return ErrOpenAnomalyExists
			}
			return err
		}
		anomalyID = anomaly.ID

		// 仅隔离已冻存（stored）于该容器内的样本；行锁顺序与容器锁一致。
		var specimens []model.Specimen
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("storage_container_id = ? AND state = ?", container.ID, constants.SpecimenStateStored).
			Find(&specimens).Error; err != nil {
			return err
		}
		reason := quarantineIsolateReason(anomaly)
		for i := range specimens {
			result.BeforeIsolated = append(result.BeforeIsolated, specimens[i])
			specimens[i].QuarantineAnomalyID = &anomaly.ID
			if err := tx.Save(&specimens[i]).Error; err != nil {
				return err
			}
			event := &model.SpecimenQuarantine{
				SpecimenID:         specimens[i].ID,
				AnomalyID:          anomaly.ID,
				Action:             constants.QuarantineActionIsolated,
				StorageContainerID: container.ID,
				OperatorID:         input.ReporterID,
				OperatorName:       strings.TrimSpace(input.ReporterName),
				Reason:             reason,
			}
			event.Normalize()
			if err := event.Validate(); err != nil {
				return err
			}
			if err := tx.Create(event).Error; err != nil {
				return err
			}
			result.AfterIsolated = append(result.AfterIsolated, specimens[i])
		}
		anomaly.AffectedCount = len(specimens)
		if err := tx.Save(anomaly).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	anomaly, err := r.Find(ctx, anomalyID)
	if err != nil {
		return nil, err
	}
	result.Anomaly = anomaly
	return result, nil
}

func (r *anomalyRepository) Release(ctx context.Context, anomalyID uint, resolverID uint, resolverName, basis string) (*AnomalyReleaseResult, error) {
	result := &AnomalyReleaseResult{}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var anomaly model.TemperatureAnomaly
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&anomaly, anomalyID).Error; err != nil {
			return err
		}
		if anomaly.Status != constants.AnomalyStateOpen {
			return ErrAnomalyNotOpen
		}
		// 锁定容器，与交接受理串行：保证解除期间新调入并隔离的样本不会被漏掉。
		var container model.StorageContainer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&container, anomaly.StorageContainerID).Error; err != nil {
			return err
		}

		var specimens []model.Specimen
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("quarantine_anomaly_id = ?", anomaly.ID).
			Find(&specimens).Error; err != nil {
			return err
		}
		releasedAt := time.Now().UTC()
		releaseReason := quarantineReleaseReason(strings.TrimSpace(basis))
		for i := range specimens {
			result.BeforeReleased = append(result.BeforeReleased, specimens[i])
			specimens[i].QuarantineAnomalyID = nil
			if err := tx.Save(&specimens[i]).Error; err != nil {
				return err
			}
			event := &model.SpecimenQuarantine{
				SpecimenID:         specimens[i].ID,
				AnomalyID:          anomaly.ID,
				Action:             constants.QuarantineActionReleased,
				StorageContainerID: anomaly.StorageContainerID,
				OperatorID:         resolverID,
				OperatorName:       strings.TrimSpace(resolverName),
				Reason:             releaseReason,
			}
			event.Normalize()
			if err := event.Validate(); err != nil {
				return err
			}
			if err := tx.Create(event).Error; err != nil {
				return err
			}
			result.AfterReleased = append(result.AfterReleased, specimens[i])
		}

		anomaly.Status = constants.AnomalyStateReleased
		anomaly.ReleasedByID = &resolverID
		anomaly.ReleasedByName = strings.TrimSpace(resolverName)
		anomaly.ReleaseBasis = strings.TrimSpace(basis)
		anomaly.ReleasedAt = &releasedAt
		anomaly.Normalize()
		if err := anomaly.Validate(); err != nil {
			return err
		}
		return tx.Save(&anomaly).Error
	})
	if err != nil {
		return nil, err
	}
	anomaly, err := r.Find(ctx, anomalyID)
	if err != nil {
		return nil, err
	}
	result.Anomaly = anomaly
	return result, nil
}

func (r *anomalyRepository) OpenSummaries(ctx context.Context, containerIDs []uint) (map[uint]AnomalyContainerSummary, error) {
	summaries := make(map[uint]AnomalyContainerSummary)
	if len(containerIDs) == 0 {
		return summaries, nil
	}
	var rows []AnomalyContainerSummary
	err := r.db.WithContext(ctx).Table("temperature_anomalies AS a").
		Select("a.id AS anomaly_id, a.storage_container_id AS container_id, COUNT(s.id) AS isolated_count").
		Joins("JOIN specimens s ON s.quarantine_anomaly_id = a.id AND s.state = ?", constants.SpecimenStateStored).
		Where("a.status = ? AND a.storage_container_id IN ?", constants.AnomalyStateOpen, containerIDs).
		Group("a.id, a.storage_container_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	// 没有隔离样本的未结异常也要暴露异常状态。
	var openOnly []AnomalyContainerSummary
	if err := r.db.WithContext(ctx).Table("temperature_anomalies AS a").
		Select("a.id AS anomaly_id, a.storage_container_id AS container_id, 0 AS isolated_count").
		Where("a.status = ? AND a.storage_container_id IN ?", constants.AnomalyStateOpen, containerIDs).
		Scan(&openOnly).Error; err != nil {
		return nil, err
	}
	for _, row := range openOnly {
		summaries[row.ContainerID] = AnomalyContainerSummary{AnomalyID: row.AnomalyID, ContainerID: row.ContainerID}
	}
	for _, row := range rows {
		summaries[row.ContainerID] = row
	}
	return summaries, nil
}

// openAnomalyForContainer 在给定事务内读取容器的未结异常。调用方须持有容器行锁。
func openAnomalyForContainer(tx *gorm.DB, containerID uint) (*model.TemperatureAnomaly, error) {
	var anomaly model.TemperatureAnomaly
	err := tx.Where("storage_container_id = ? AND status = ?", containerID, constants.AnomalyStateOpen).
		First(&anomaly).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &anomaly, nil
}

// appendQuarantineEvent 在给定事务内追加一条样本隔离历史。
func appendQuarantineEvent(tx *gorm.DB, event *model.SpecimenQuarantine) error {
	event.Normalize()
	if err := event.Validate(); err != nil {
		return err
	}
	return tx.Create(event).Error
}

func quarantineIsolateReason(anomaly *model.TemperatureAnomaly) string {
	description := strings.TrimSpace(anomaly.Description)
	if description != "" {
		return description
	}
	return fmt.Sprintf("巡检温度 %.2f°C 超出温区，按异常单 %s 隔离", anomaly.TemperatureC, anomaly.AnomalyNo)
}

func quarantineReleaseReason(basis string) string {
	return "异常解除：" + basis
}
