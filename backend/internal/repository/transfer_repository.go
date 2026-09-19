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
	ErrTransferAlreadyResolved = errors.New("custody transfer is already resolved")
	ErrSpecimenCustodyChanged  = errors.New("specimen custody changed after transfer preparation")
	ErrTargetContainerFull     = errors.New("target storage container is not available or is full")
	ErrPositionOccupied        = errors.New("target storage position is already occupied")
	ErrTemperatureExcursion    = errors.New("recorded temperature is outside the target container range")
	ErrPreparedTransferExists  = errors.New("specimen already has a prepared transfer")
	ErrSpecimenQuarantined     = errors.New("specimen is quarantined by an open temperature anomaly")
)

type TransferFilter struct {
	dto.PageQuery
	State      string `form:"state"`
	SpecimenID uint   `form:"specimenId"`
}

type TransferResolution struct {
	State          constants.TransferState
	ToContainerID  *uint
	ToPosition     string
	TemperatureC   *float64
	Reason         string
	ResolvedByID   uint
	ResolvedByName string
	ResolvedAt     time.Time
}

// SpecimenQuarantineResult 携带交接事务内触发的自动隔离副作用，供服务层补审计事件。
type SpecimenQuarantineResult struct {
	Before model.Specimen
	After  model.Specimen
	Event  model.SpecimenQuarantine
}

type TransferRepository interface {
	List(context.Context, TransferFilter) ([]model.CustodyTransfer, int64, error)
	Find(context.Context, uint) (*model.CustodyTransfer, error)
	FindByNumber(context.Context, string) (*model.CustodyTransfer, error)
	Create(context.Context, *model.CustodyTransfer) error
	CountPreparedForSpecimen(context.Context, uint) (int64, error)
	Resolve(context.Context, uint, TransferResolution) (*model.CustodyTransfer, *model.Specimen, model.Specimen, []SpecimenQuarantineResult, error)
}

type transferRepository struct{ db *gorm.DB }

func NewTransferRepository(db *gorm.DB) TransferRepository {
	return &transferRepository{db: db}
}

func (r *transferRepository) List(ctx context.Context, filter TransferFilter) ([]model.CustodyTransfer, int64, error) {
	query := filter.PageQuery.Normalize()
	db := r.db.WithContext(ctx).Model(&model.CustodyTransfer{})
	if state := strings.TrimSpace(filter.State); state != "" {
		db = db.Where("state = ?", state)
	}
	if filter.SpecimenID > 0 {
		db = db.Where("specimen_id = ?", filter.SpecimenID)
	}
	if search := strings.TrimSpace(query.Search); search != "" {
		like := "%" + search + "%"
		db = db.Where("transfer_no ILIKE ? OR from_custodian ILIKE ? OR to_custodian ILIKE ? OR from_location ILIKE ? OR to_location ILIKE ?", like, like, like, like, like)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]model.CustodyTransfer, 0)
	err := db.Preload("Specimen").Preload("Specimen.StorageContainer").Preload("ToContainer").
		Order("prepared_at DESC, id DESC").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&items).Error
	return items, total, err
}

func (r *transferRepository) Find(ctx context.Context, id uint) (*model.CustodyTransfer, error) {
	var item model.CustodyTransfer
	err := r.db.WithContext(ctx).Preload("Specimen").Preload("Specimen.StorageContainer").Preload("ToContainer").First(&item, id).Error
	return &item, err
}

func (r *transferRepository) FindByNumber(ctx context.Context, number string) (*model.CustodyTransfer, error) {
	var item model.CustodyTransfer
	err := r.db.WithContext(ctx).Where("transfer_no = ?", number).First(&item).Error
	return &item, err
}

func (r *transferRepository) Create(ctx context.Context, item *model.CustodyTransfer) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var specimen model.Specimen
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&specimen, item.SpecimenID).Error; err != nil {
			return err
		}
		if specimen.State.Terminal() {
			return ErrSpecimenCustodyChanged
		}
		// 隔离样本禁止发起交接：与异常建单/解除在样本行上串行，避免漏隔离或重复单。
		if specimen.QuarantineAnomalyID != nil && *specimen.QuarantineAnomalyID > 0 {
			return ErrSpecimenQuarantined
		}
		var prepared int64
		if err := tx.Model(&model.CustodyTransfer{}).
			Where("specimen_id = ? AND state = ?", specimen.ID, constants.TransferStatePrepared).
			Count(&prepared).Error; err != nil {
			return err
		}
		if prepared > 0 {
			return ErrPreparedTransferExists
		}
		return tx.Create(item).Error
	})
}

func (r *transferRepository) CountPreparedForSpecimen(ctx context.Context, specimenID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.CustodyTransfer{}).
		Where("specimen_id = ? AND state = ?", specimenID, constants.TransferStatePrepared).Count(&count).Error
	return count, err
}

// sortedContainerIDs 返回去重后按升序排列的容器 ID，用于统一多容器加锁顺序。
func sortedContainerIDs(ids ...*uint) []uint {
	seen := make(map[uint]struct{})
	for _, id := range ids {
		if id != nil && *id > 0 {
			seen[*id] = struct{}{}
		}
	}
	result := make([]uint, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j] < result[i] {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

func (r *transferRepository) Resolve(ctx context.Context, transferID uint, resolution TransferResolution) (*model.CustodyTransfer, *model.Specimen, model.Specimen, []SpecimenQuarantineResult, error) {
	var transfer model.CustodyTransfer
	var specimen model.Specimen
	var before model.Specimen
	quarantineResults := make([]SpecimenQuarantineResult, 0)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&transfer, transferID).Error; err != nil {
			return err
		}
		if transfer.State != constants.TransferStatePrepared {
			return ErrTransferAlreadyResolved
		}
		// 拒绝/取消仅流转单据，不移动样本，隔离样本也允许完成这两类处理。
		if resolution.State != constants.TransferStateAccepted {
			transfer.State = resolution.State
			transfer.ToContainerID = nil
			transfer.ToPosition = ""
			transfer.TemperatureC = resolution.TemperatureC
			transfer.Reason = strings.TrimSpace(resolution.Reason)
			transfer.AcceptedByID = &resolution.ResolvedByID
			transfer.AcceptedByName = strings.TrimSpace(resolution.ResolvedByName)
			transfer.ResolvedAt = &resolution.ResolvedAt
			transfer.Normalize()
			if err := transfer.Validate(); err != nil {
				return fmt.Errorf("validate resolved transfer: %w", err)
			}
			return tx.Save(&transfer).Error
		}

		if resolution.ToContainerID == nil || *resolution.ToContainerID == 0 {
			return ErrTargetContainerFull
		}
		// 先无锁读取样本来源容器，用于统一容器加锁顺序。
		var probe model.Specimen
		if err := tx.Select("id", "storage_container_id").First(&probe, transfer.SpecimenID).Error; err != nil {
			return err
		}
		// 锁顺序全局统一为「容器（按 id 升序）→ 样本」，杜绝与异常建单/解除交叉死锁。
		containerIDs := sortedContainerIDs(probe.StorageContainerID, resolution.ToContainerID)
		containers := make(map[uint]model.StorageContainer, len(containerIDs))
		for _, containerID := range containerIDs {
			var lockedContainer model.StorageContainer
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedContainer, containerID).Error; err != nil {
				return err
			}
			containers[containerID] = lockedContainer
		}
		target := containers[*resolution.ToContainerID]

		if err := tx.Preload("StorageContainer").Clauses(clause.Locking{Strength: "UPDATE"}).First(&specimen, transfer.SpecimenID).Error; err != nil {
			return err
		}
		before = specimen
		if specimen.CurrentCustodian != transfer.FromCustodian || specimen.LocationLabel() != transfer.FromLocation {
			return ErrSpecimenCustodyChanged
		}
		// 来源容器挂有未结异常时，隔离样本不能调出。
		if specimen.QuarantineAnomalyID != nil && *specimen.QuarantineAnomalyID > 0 {
			return ErrSpecimenQuarantined
		}

		transfer.State = resolution.State
		transfer.ToContainerID = &target.ID
		transfer.ToPosition = strings.TrimSpace(resolution.ToPosition)
		transfer.TemperatureC = resolution.TemperatureC
		transfer.Reason = strings.TrimSpace(resolution.Reason)
		transfer.AcceptedByID = &resolution.ResolvedByID
		transfer.AcceptedByName = strings.TrimSpace(resolution.ResolvedByName)
		transfer.ResolvedAt = &resolution.ResolvedAt
		transfer.Normalize()

		sameContainer := specimen.StorageContainerID != nil && *specimen.StorageContainerID == target.ID
		if !target.CanReceive() && !sameContainer {
			return ErrTargetContainerFull
		}
		if transfer.TemperatureC != nil && !target.AcceptsTemperature(*transfer.TemperatureC) {
			return ErrTemperatureExcursion
		}
		var occupied int64
		positionQuery := tx.Model(&model.Specimen{}).
			Where("storage_container_id = ? AND position = ? AND id <> ? AND state NOT IN ?", target.ID, transfer.ToPosition, specimen.ID, []constants.SpecimenState{constants.SpecimenStateReleased, constants.SpecimenStateDisposed})
		if err := positionQuery.Count(&occupied).Error; err != nil {
			return err
		}
		if occupied > 0 {
			return ErrPositionOccupied
		}
		oldContainerID := specimen.StorageContainerID
		if oldContainerID == nil || *oldContainerID != target.ID {
			if oldContainerID != nil && *oldContainerID > 0 {
				if err := tx.Model(&model.StorageContainer{}).Where("id = ?", *oldContainerID).
					UpdateColumn("occupied", gorm.Expr("GREATEST(occupied - 1, 0)")).Error; err != nil {
					return err
				}
			}
			result := tx.Model(&model.StorageContainer{}).
				Where("id = ? AND active = ? AND status = ? AND occupied < capacity", target.ID, true, "available").
				UpdateColumn("occupied", gorm.Expr("occupied + 1"))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrTargetContainerFull
			}
		}
		specimen.StorageContainerID = &target.ID
		specimen.StorageContainer = &target
		specimen.Position = transfer.ToPosition
		specimen.CurrentCustodian = transfer.ToCustodian
		if specimen.State == constants.SpecimenStateReceived || specimen.State == constants.SpecimenStateAliquoted {
			specimen.State = constants.SpecimenStateStored
		}
		if err := specimen.Validate(); err != nil {
			return fmt.Errorf("validate moved specimen: %w", err)
		}

		// 目标容器存在未结异常时，调入的已冻存样本必须立即隔离，保证不漏隔离。
		openAnomaly, err := openAnomalyForContainer(tx, target.ID)
		if err != nil {
			return err
		}
		if openAnomaly != nil && specimen.State == constants.SpecimenStateStored {
			specimen.QuarantineAnomalyID = &openAnomaly.ID
			event := &model.SpecimenQuarantine{
				SpecimenID:         specimen.ID,
				AnomalyID:          openAnomaly.ID,
				Action:             constants.QuarantineActionIsolated,
				StorageContainerID: target.ID,
				OperatorID:         resolution.ResolvedByID,
				OperatorName:       strings.TrimSpace(resolution.ResolvedByName),
				Reason:             "交接受理时目标容器存在未结异常 " + openAnomaly.AnomalyNo + "，自动隔离",
			}
			if err := appendQuarantineEvent(tx, event); err != nil {
				return err
			}
			quarantineBefore := before
			if err := tx.Save(&specimen).Error; err != nil {
				return err
			}
			quarantineResults = append(quarantineResults, SpecimenQuarantineResult{
				Before: quarantineBefore,
				After:  specimen,
				Event:  *event,
			})
		} else if err := tx.Save(&specimen).Error; err != nil {
			return err
		}
		if err := transfer.Validate(); err != nil {
			return fmt.Errorf("validate resolved transfer: %w", err)
		}
		return tx.Save(&transfer).Error
	})
	if err != nil {
		return nil, nil, model.Specimen{}, nil, err
	}
	resolved, err := r.Find(ctx, transfer.ID)
	if err != nil {
		return nil, nil, model.Specimen{}, nil, err
	}
	return resolved, &specimen, before, quarantineResults, nil
}
