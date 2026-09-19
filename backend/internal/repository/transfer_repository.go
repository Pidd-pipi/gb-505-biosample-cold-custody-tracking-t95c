package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"biosample-cold-custody-tracking/backend/internal/constants"
	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/model"
)

var (
	ErrTransferAlreadyResolved    = errors.New("custody transfer is already resolved")
	ErrSpecimenCustodyChanged     = errors.New("specimen custody changed after transfer preparation")
	ErrTargetContainerFull        = errors.New("target storage container is not available or is full")
	ErrTargetContainerQuarantined = errors.New("target storage container is quarantined by an open temperature anomaly")
	ErrPositionOccupied           = errors.New("target storage position is already occupied")
	ErrTemperatureExcursion       = errors.New("recorded temperature is outside the target container range")
	ErrSpecimenUnavailable        = errors.New("specimen cannot accept a new transfer")
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

type TransferRepository interface {
	List(context.Context, TransferFilter) ([]model.CustodyTransfer, int64, error)
	Find(context.Context, uint) (*model.CustodyTransfer, error)
	FindByNumber(context.Context, string) (*model.CustodyTransfer, error)
	Create(context.Context, *model.CustodyTransfer) error
	// CreateLocked re-validates prepared-transfer and isolation invariants
	// against a row-locked specimen so concurrent patrols cannot race.
	CreateLocked(context.Context, *model.CustodyTransfer) error
	CountPreparedForSpecimen(context.Context, uint) (int64, error)
	Resolve(context.Context, uint, TransferResolution) (*model.CustodyTransfer, *model.Specimen, model.Specimen, error)
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
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *transferRepository) CreateLocked(ctx context.Context, item *model.CustodyTransfer) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var specimen model.Specimen
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&specimen, item.SpecimenID).Error; err != nil {
			return err
		}
		if specimen.Isolated {
			return ErrSpecimenIsolated
		}
		if specimen.State.Terminal() {
			return ErrSpecimenUnavailable
		}
		var prepared int64
		if err := tx.Model(&model.CustodyTransfer{}).
			Where("specimen_id = ? AND state = ?", item.SpecimenID, constants.TransferStatePrepared).
			Count(&prepared).Error; err != nil {
			return err
		}
		if prepared > 0 {
			return ErrSpecimenUnavailable
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

func (r *transferRepository) Resolve(ctx context.Context, transferID uint, resolution TransferResolution) (*model.CustodyTransfer, *model.Specimen, model.Specimen, error) {
	var transfer model.CustodyTransfer
	var specimen model.Specimen
	var before model.Specimen
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Read the transfer first without locking to learn which containers are
		// involved; advisory locks are always taken in ascending id order.
		if err := tx.First(&transfer, transferID).Error; err != nil {
			return err
		}
		if transfer.State != constants.TransferStatePrepared {
			return ErrTransferAlreadyResolved
		}
		var probe model.Specimen
		if err := tx.First(&probe, transfer.SpecimenID).Error; err != nil {
			return err
		}
		lockIDs := sortedContainerLockIDs(probe.StorageContainerID, resolution.ToContainerID)
		for _, lockID := range lockIDs {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", lockID).Error; err != nil {
				return err
			}
		}

		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&transfer, transferID).Error; err != nil {
			return err
		}
		if transfer.State != constants.TransferStatePrepared {
			return ErrTransferAlreadyResolved
		}
		if err := tx.Preload("StorageContainer").Clauses(clause.Locking{Strength: "UPDATE"}).First(&specimen, transfer.SpecimenID).Error; err != nil {
			return err
		}
		before = specimen
		if specimen.CurrentCustodian != transfer.FromCustodian || specimen.LocationLabel() != transfer.FromLocation {
			return ErrSpecimenCustodyChanged
		}
		// A patrol report can quarantine the specimen between preparation and
		// resolution; isolation forbids every transfer operation.
		if specimen.Isolated {
			return ErrSpecimenIsolated
		}

		transfer.State = resolution.State
		transfer.ToContainerID = resolution.ToContainerID
		transfer.ToPosition = strings.TrimSpace(resolution.ToPosition)
		transfer.TemperatureC = resolution.TemperatureC
		transfer.Reason = strings.TrimSpace(resolution.Reason)
		transfer.AcceptedByID = &resolution.ResolvedByID
		transfer.AcceptedByName = strings.TrimSpace(resolution.ResolvedByName)
		transfer.ResolvedAt = &resolution.ResolvedAt
		transfer.Normalize()

		if transfer.State == constants.TransferStateAccepted {
			if transfer.ToContainerID == nil || *transfer.ToContainerID == 0 {
				return ErrTargetContainerFull
			}
			var target model.StorageContainer
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, *transfer.ToContainerID).Error; err != nil {
				return err
			}
			// A container with an open anomaly cannot receive specimens, even
			// when the specimen merely moves to another slot in that container.
			if openAnomaly, err := openAnomalyCount(ctx, tx, target.ID); err != nil {
				return err
			} else if openAnomaly > 0 {
				return ErrTargetContainerQuarantined
			}
			if !target.CanReceive() && (specimen.StorageContainerID == nil || *specimen.StorageContainerID != target.ID) {
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
			if err := tx.Save(&specimen).Error; err != nil {
				return err
			}
		}
		if err := transfer.Validate(); err != nil {
			return fmt.Errorf("validate resolved transfer: %w", err)
		}
		return tx.Save(&transfer).Error
	})
	if err != nil {
		return nil, nil, model.Specimen{}, err
	}
	resolved, err := r.Find(ctx, transfer.ID)
	if err != nil {
		return nil, nil, model.Specimen{}, err
	}
	return resolved, &specimen, before, nil
}

// sortedContainerLockIDs returns the advisory-lock ids for the distinct
// involved containers in ascending order, preventing cross-transaction
// deadlocks between patrol reports and transfer resolutions.
func sortedContainerLockIDs(containerIDs ...*uint) []int64 {
	seen := make(map[uint]struct{})
	ids := make([]int64, 0, len(containerIDs))
	for _, containerID := range containerIDs {
		if containerID == nil || *containerID == 0 {
			continue
		}
		if _, ok := seen[*containerID]; ok {
			continue
		}
		seen[*containerID] = struct{}{}
		ids = append(ids, containerAdvisoryLockID(*containerID))
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func openAnomalyCount(ctx context.Context, tx *gorm.DB, containerID uint) (int64, error) {
	var count int64
	err := tx.WithContext(ctx).Model(&model.TemperatureAnomaly{}).
		Where("storage_container_id = ? AND state = ?", containerID, constants.AnomalyStateOpen).
		Count(&count).Error
	return count, err
}
