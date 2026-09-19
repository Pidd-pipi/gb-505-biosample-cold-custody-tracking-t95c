package service

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"biosample-cold-custody-tracking/backend/internal/constants"
	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/model"
	"biosample-cold-custody-tracking/backend/internal/repository"
	"biosample-cold-custody-tracking/backend/internal/util"
)

type StorageService interface {
	List(context.Context, repository.StorageFilter) (dto.PageResult[model.StorageContainer], error)
	Get(context.Context, uint) (*model.StorageContainer, error)
	Create(context.Context, Actor, dto.CreateStorageRequest) (*model.StorageContainer, error)
	Update(context.Context, Actor, uint, dto.UpdateStorageRequest) (*model.StorageContainer, error)
}

type storageService struct {
	repo        repository.StorageRepository
	anomalyRepo repository.AnomalyRepository
	audit       AuditService
}

func NewStorageService(repo repository.StorageRepository, anomalyRepo repository.AnomalyRepository, audit AuditService) StorageService {
	return &storageService{repo: repo, anomalyRepo: anomalyRepo, audit: audit}
}

func (s *storageService) List(ctx context.Context, filter repository.StorageFilter) (dto.PageResult[model.StorageContainer], error) {
	query := filter.PageQuery.Normalize()
	filter.PageQuery = query
	items, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return dto.PageResult[model.StorageContainer]{}, err
	}
	if err := s.attachAnomalies(ctx, items); err != nil {
		return dto.PageResult[model.StorageContainer]{}, err
	}
	return dto.PageResult[model.StorageContainer]{Items: items, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
}

func (s *storageService) Get(ctx context.Context, id uint) (*model.StorageContainer, error) {
	item, err := s.repo.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	items := []model.StorageContainer{*item}
	if err := s.attachAnomalies(ctx, items); err != nil {
		return nil, err
	}
	return &items[0], nil
}

// attachAnomalies 为容器填充未结异常状态与当前隔离样本数，供冻存页直接展示。
func (s *storageService) attachAnomalies(ctx context.Context, items []model.StorageContainer) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	summaries, err := s.anomalyRepo.OpenSummaries(ctx, ids)
	if err != nil {
		return err
	}
	for i := range items {
		summary, ok := summaries[items[i].ID]
		if !ok {
			continue
		}
		anomalyID := summary.AnomalyID
		items[i].OpenAnomalyID = &anomalyID
		items[i].AnomalyStatus = string(constants.AnomalyStateOpen)
		items[i].IsolatedSpecimenCount = int(summary.IsolatedCount)
	}
	return nil
}

func (s *storageService) Create(ctx context.Context, actor Actor, input dto.CreateStorageRequest) (*model.StorageContainer, error) {
	code := strings.ToUpper(strings.TrimSpace(input.Code))
	if _, err := s.repo.FindByCode(ctx, code); err == nil {
		return nil, util.Conflict("冻存容器编码已存在")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = "available"
	}
	item := &model.StorageContainer{
		Code:            code,
		Name:            input.Name,
		ContainerType:   input.ContainerType,
		TemperatureZone: input.TemperatureZone,
		Location:        input.Location,
		Capacity:        input.Capacity,
		Occupied:        0,
		Status:          status,
		Active:          true,
	}
	item.Normalize()
	if err := item.Validate(); err != nil {
		return nil, util.BadRequest(err.Error())
	}
	if err := s.repo.Create(ctx, item); err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, actor, "storage_container.created", "StorageContainer", item.ID, nil, item); err != nil {
		return nil, err
	}
	return s.repo.Find(ctx, item.ID)
}

func (s *storageService) Update(ctx context.Context, actor Actor, id uint, input dto.UpdateStorageRequest) (*model.StorageContainer, error) {
	item, err := s.repo.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	before := *item
	if input.Name != nil {
		item.Name = *input.Name
	}
	if input.ContainerType != nil {
		item.ContainerType = *input.ContainerType
	}
	if input.TemperatureZone != nil {
		if item.Occupied > 0 && *input.TemperatureZone != item.TemperatureZone {
			return nil, util.Conflict("容器内仍有样本，不能变更温区")
		}
		item.TemperatureZone = *input.TemperatureZone
	}
	if input.Location != nil {
		item.Location = *input.Location
	}
	if input.Capacity != nil {
		if *input.Capacity < item.Occupied {
			return nil, util.Conflict("容量不能低于当前占用数")
		}
		item.Capacity = *input.Capacity
	}
	if input.Status != nil {
		item.Status = *input.Status
	}
	if input.Active != nil {
		if !*input.Active {
			count, countErr := s.repo.CountStoredSpecimens(ctx, id)
			if countErr != nil {
				return nil, countErr
			}
			if count > 0 {
				return nil, util.Conflict("容器内仍有保管中的样本，不能停用")
			}
		}
		item.Active = *input.Active
	}
	item.Normalize()
	if err := item.Validate(); err != nil {
		return nil, util.BadRequest(err.Error())
	}
	if err := s.repo.Save(ctx, item); err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, actor, "storage_container.updated", "StorageContainer", item.ID, before, item); err != nil {
		return nil, err
	}
	return s.repo.Find(ctx, item.ID)
}
