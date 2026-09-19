package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/model"
	"biosample-cold-custody-tracking/backend/internal/repository"
	"biosample-cold-custody-tracking/backend/internal/util"
)

type AnomalyService interface {
	List(context.Context, repository.AnomalyFilter) (dto.PageResult[model.TemperatureAnomaly], error)
	Get(context.Context, uint) (*model.TemperatureAnomaly, error)
	Report(context.Context, Actor, dto.CreateTemperatureAnomalyRequest) (*model.TemperatureAnomaly, error)
	Resolve(context.Context, Actor, uint, dto.ResolveTemperatureAnomalyRequest) (*model.TemperatureAnomaly, error)
}

type anomalyService struct {
	repo        repository.AnomalyRepository
	storageRepo repository.StorageRepository
	audit       AuditService
}

func NewAnomalyService(repo repository.AnomalyRepository, storageRepo repository.StorageRepository, audit AuditService) AnomalyService {
	return &anomalyService{repo: repo, storageRepo: storageRepo, audit: audit}
}

func (s *anomalyService) List(ctx context.Context, filter repository.AnomalyFilter) (dto.PageResult[model.TemperatureAnomaly], error) {
	query := filter.PageQuery.Normalize()
	filter.PageQuery = query
	items, total, err := s.repo.List(ctx, filter)
	return dto.PageResult[model.TemperatureAnomaly]{Items: items, Total: total, Page: query.Page, PageSize: query.PageSize}, err
}

func (s *anomalyService) Get(ctx context.Context, id uint) (*model.TemperatureAnomaly, error) {
	return s.repo.Find(ctx, id)
}

func (s *anomalyService) Report(ctx context.Context, actor Actor, input dto.CreateTemperatureAnomalyRequest) (*model.TemperatureAnomaly, error) {
	container, err := s.storageRepo.Find(ctx, input.StorageContainerID)
	if err != nil {
		return nil, err
	}
	if !container.Active {
		return nil, util.Conflict("冻存容器已停用，不能登记巡检读数")
	}
	readingAt := input.ReadingAt.UTC()
	if readingAt.After(time.Now().Add(5 * time.Minute)) {
		return nil, util.BadRequest("巡检时间不能晚于当前时间")
	}
	if container.AcceptsTemperature(input.RecordedC) {
		return nil, util.Conflict("读数在容器温区范围内，不构成冷链超限异常")
	}
	number := s.repo.NextNumber(ctx, readingAt)
	anomaly := &model.TemperatureAnomaly{
		AnomalyNo:          number,
		StorageContainerID: container.ID,
		RecordedC:          input.RecordedC,
		ReadingAt:          readingAt,
		State:              "open",
		Description:        strings.TrimSpace(input.Description),
		ReportedByID:       actor.ID,
		ReportedByName:     actor.Name,
		ReportedAt:         time.Now().UTC(),
	}
	anomaly.Normalize()
	if err := anomaly.Validate(); err != nil {
		return nil, util.BadRequest(err.Error())
	}
	emit := func(tx *gorm.DB, draft repository.AuditDraft) error {
		return s.audit.RecordInTx(ctx, tx, actor, draft)
	}
	created, err := s.repo.Report(ctx, anomaly, emit)
	if errors.Is(err, repository.ErrAnomalyAlreadyOpen) {
		return nil, util.Conflict("该冻存容器已有未结冷链异常，不能重复建单")
	}
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *anomalyService) Resolve(ctx context.Context, actor Actor, id uint, input dto.ResolveTemperatureAnomalyRequest) (*model.TemperatureAnomaly, error) {
	existing, err := s.repo.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if !existing.IsOpen() {
		return nil, util.Conflict("冷链异常已结单，不能重复处理")
	}
	basis := strings.TrimSpace(input.ResolutionBasis)
	if len([]rune(basis)) < 5 {
		return nil, util.BadRequest("解除或判废必须填写至少 5 个字符的处理依据")
	}
	resolution := repository.AnomalyResolutionInput{
		Decision:        input.Decision,
		RecoveredC:      input.RecoveredC,
		ResolutionBasis: basis,
		ResolvedByID:    actor.ID,
		ResolvedByName:  actor.Name,
		ResolvedAt:      time.Now().UTC(),
	}
	emit := func(tx *gorm.DB, draft repository.AuditDraft) error {
		return s.audit.RecordInTx(ctx, tx, actor, draft)
	}
	resolved, err := s.repo.Close(ctx, id, resolution, emit)
	switch {
	case errors.Is(err, repository.ErrAnomalyNotOpen):
		return nil, util.Conflict("冷链异常已被其他请求处理")
	case errors.Is(err, repository.ErrAnomalyCannotSelfClose):
		return nil, util.Forbidden("异常建单人不能自行解除，必须由其他保管员填写依据后处理")
	case errors.Is(err, repository.ErrAnomalyRecoveryRequired):
		return nil, util.BadRequest("确认温度恢复时必须填写复核温度读数")
	case errors.Is(err, repository.ErrAnomalyStillOutOfRange):
		return nil, util.Conflict("复核温度仍超出容器温区，不能解除隔离")
	case err != nil:
		return nil, err
	}
	return resolved, nil
}
