package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/model"
	"biosample-cold-custody-tracking/backend/internal/repository"
	"biosample-cold-custody-tracking/backend/internal/util"
)

type AnomalyService interface {
	List(context.Context, repository.AnomalyFilter) (dto.PageResult[model.TemperatureAnomaly], error)
	Get(context.Context, uint) (*model.TemperatureAnomaly, error)
	Report(context.Context, Actor, dto.CreateAnomalyRequest) (*model.TemperatureAnomaly, error)
	Release(context.Context, Actor, uint, dto.ReleaseAnomalyRequest) (*model.TemperatureAnomaly, error)
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

func (s *anomalyService) Report(ctx context.Context, actor Actor, input dto.CreateAnomalyRequest) (*model.TemperatureAnomaly, error) {
	container, err := s.storageRepo.Find(ctx, input.StorageContainerID)
	if err != nil {
		return nil, err
	}
	lower, upper := container.TemperatureRange()
	if input.TemperatureC >= lower && input.TemperatureC <= upper {
		return nil, util.BadRequest("读数仍在容器温区范围内，无需建立异常单")
	}
	inspectedAt := time.Now().UTC()
	if input.InspectedAt != nil {
		inspectedAt = input.InspectedAt.UTC()
	}
	if inspectedAt.After(time.Now().Add(5 * time.Minute)) {
		return nil, util.BadRequest("巡检时间不能晚于当前时间")
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		number := generateAnomalyNo()
		result, reportErr := s.repo.Report(ctx, repository.ReportAnomalyInput{
			AnomalyNo:    number,
			ContainerID:  container.ID,
			TemperatureC: input.TemperatureC,
			InspectedAt:  inspectedAt,
			Description:  strings.TrimSpace(input.Description),
			ReporterID:   actor.ID,
			ReporterName: actor.Name,
		})
		if reportErr == nil {
			if err := s.recordReportAudit(ctx, actor, result); err != nil {
				return nil, err
			}
			return result.Anomaly, nil
		}
		switch {
		case errors.Is(reportErr, repository.ErrAnomalyNumberTaken):
			lastErr = reportErr
			continue
		case errors.Is(reportErr, repository.ErrOpenAnomalyExists):
			return nil, util.Conflict("该冻存容器已有未结冷链异常，不能重复建单")
		case errors.Is(reportErr, repository.ErrTemperatureWithinRange):
			return nil, util.BadRequest("读数仍在容器温区范围内，无需建立异常单")
		default:
			return nil, reportErr
		}
	}
	if lastErr != nil {
		return nil, util.Conflict("异常单号冲突，请稍后重试")
	}
	return nil, util.Conflict("异常建单失败，请稍后重试")
}

func (s *anomalyService) recordReportAudit(ctx context.Context, actor Actor, result *repository.AnomalyReportResult) error {
	if err := s.audit.Record(ctx, actor, "temperature_anomaly.reported", "TemperatureAnomaly", result.Anomaly.ID, nil, result.Anomaly); err != nil {
		return err
	}
	for i := range result.AfterIsolated {
		if err := s.audit.Record(ctx, actor, "specimen.quarantined", "Specimen", result.AfterIsolated[i].ID, result.BeforeIsolated[i], result.AfterIsolated[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *anomalyService) Release(ctx context.Context, actor Actor, id uint, input dto.ReleaseAnomalyRequest) (*model.TemperatureAnomaly, error) {
	existing, err := s.repo.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if !existing.Open() {
		return nil, util.Conflict("异常已解除，不能重复解除")
	}
	// 温度恢复不自动解除；必须由非建单人填写依据后手动解除。
	if actor.ID == existing.ReportedByID {
		return nil, util.Forbidden("异常建单人不能解除自己的异常单，须由其他有权限人员复核解除")
	}
	basis := strings.TrimSpace(input.ReleaseBasis)
	if len([]rune(basis)) < 3 {
		return nil, util.BadRequest("解除异常必须填写至少 3 个字符的依据")
	}
	result, err := s.repo.Release(ctx, id, actor.ID, actor.Name, basis)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrAnomalyNotOpen):
			return nil, util.Conflict("异常已被其他请求解除")
		default:
			return nil, err
		}
	}
	// 解除失败时事务已整体回滚，异常单与全部样本保持原状；审计随业务事务成功后再追加。
	if err := s.audit.Record(ctx, actor, "temperature_anomaly.released", "TemperatureAnomaly", id, existing, result.Anomaly); err != nil {
		return nil, err
	}
	for i := range result.AfterReleased {
		if err := s.audit.Record(ctx, actor, "specimen.quarantine_released", "Specimen", result.AfterReleased[i].ID, result.BeforeReleased[i], result.AfterReleased[i]); err != nil {
			return nil, err
		}
	}
	return result.Anomaly, nil
}

func generateAnomalyNo() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败极少见，退化为时间熵仍保持格式合规。
		return fmt.Sprintf("TA-%d", time.Now().UnixNano()%100000000)
	}
	return "TA-" + time.Now().UTC().Format("20060102") + "-" + strings.ToUpper(hex.EncodeToString(buf))
}
