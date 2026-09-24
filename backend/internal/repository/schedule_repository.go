package repository

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"timber-kiln-drying-optimizer/backend/internal/model"
	"time"
)

type ScheduleRepository struct{ DB *gorm.DB }

func (r ScheduleRepository) List(ctx context.Context) ([]model.DryingSchedule, error) {
	var items []model.DryingSchedule
	err := r.DB.WithContext(ctx).Order("calculated_at desc").Find(&items).Error
	return items, err
}
func (r ScheduleRepository) Get(ctx context.Context, id string) (model.DryingSchedule, error) {
	var item model.DryingSchedule
	err := r.DB.WithContext(ctx).First(&item, "id = ?", id).Error
	if err != nil {
		return item, fmt.Errorf("get schedule: %w", err)
	}
	return item, nil
}
func (r ScheduleRepository) ByHash(ctx context.Context, lotID, hash, algorithm string) (model.DryingSchedule, error) {
	var item model.DryingSchedule
	err := r.DB.WithContext(ctx).Where("timber_lot_id = ? AND input_hash = ? AND algorithm_version = ?", lotID, hash, algorithm).First(&item).Error
	return item, err
}

// LatestFrozenBaseline returns the most recently frozen plan for a lot, if one
// exists, so a live plan can show its completion-time and risk gap.
func (r ScheduleRepository) LatestFrozenBaseline(ctx context.Context, lotID string) (model.DryingSchedule, error) {
	var item model.DryingSchedule
	err := r.DB.WithContext(ctx).
		Where("timber_lot_id = ? AND frozen_at IS NOT NULL", lotID).
		Order("frozen_at desc").First(&item).Error
	return item, err
}

// LatestFrozenBaselines returns the most recently frozen plan keyed by lot ID.
func (r ScheduleRepository) LatestFrozenBaselines(ctx context.Context) (map[string]model.DryingSchedule, error) {
	var frozen []model.DryingSchedule
	if err := r.DB.WithContext(ctx).Where("frozen_at IS NOT NULL").Order("frozen_at asc").Find(&frozen).Error; err != nil {
		return nil, err
	}
	baselines := map[string]model.DryingSchedule{}
	for _, item := range frozen {
		baselines[item.TimberLotID] = item
	}
	return baselines, nil
}
func (r ScheduleRepository) ByIdempotencyKey(ctx context.Context, key string) (model.DryingSchedule, error) {
	var item model.DryingSchedule
	err := r.DB.WithContext(ctx).Where("idempotency_key = ?", key).First(&item).Error
	return item, err
}
func (r ScheduleRepository) Create(ctx context.Context, item *model.DryingSchedule) error {
	return r.DB.WithContext(ctx).Create(item).Error
}
func (r ScheduleRepository) Save(ctx context.Context, item *model.DryingSchedule) error {
	return r.DB.WithContext(ctx).Save(item).Error
}

func (r ScheduleRepository) Transition(ctx context.Context, id, current, next string, version int, updates map[string]any) (bool, error) {
	updates["schedule_state"] = next
	updates["version"] = version + 1
	result := r.DB.WithContext(ctx).Model(&model.DryingSchedule{}).
		Where("id = ? AND schedule_state = ? AND version = ?", id, current, version).
		Updates(updates)
	return result.RowsAffected == 1, result.Error
}

func (r ScheduleRepository) Freeze(ctx context.Context, id string, version int, actor string, at time.Time, snapshot string) (bool, error) {
	result := r.DB.WithContext(ctx).Model(&model.DryingSchedule{}).
		Where("id = ? AND version = ? AND frozen_at IS NULL", id, version).
		Updates(map[string]any{"frozen_at": at, "frozen_by": actor, "frozen_snapshot": snapshot, "version": version + 1})
	return result.RowsAffected == 1, result.Error
}

func (r ScheduleRepository) SaveComparison(ctx context.Context, id string, version int, baselineID, comparison string) (bool, error) {
	result := r.DB.WithContext(ctx).Model(&model.DryingSchedule{}).
		Where("id = ? AND version = ?", id, version).
		Updates(map[string]any{"baseline_schedule_id": baselineID, "comparison_json": comparison, "version": version + 1})
	return result.RowsAffected == 1, result.Error
}
