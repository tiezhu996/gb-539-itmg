package algorithm

import (
	"testing"
	"time"

	"timber-kiln-drying-optimizer/backend/internal/constants"
	"timber-kiln-drying-optimizer/backend/internal/model"
)

func pineFiberEnvelope(t *testing.T) (model.TimberLot, model.DryingKiln, Rule, model.MoistureReading, model.MoistureReading, model.MoistureReading, model.MoistureReading, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lot := model.TimberLot{Species: "pine", ThicknessMM: 30, TargetMoisturePct: 12, InitialMoisturePct: 34, LoadedAt: now.Add(-20 * time.Hour)}
	kiln := model.DryingKiln{MaxTemperatureC: 70, MinHumidityPct: 30}
	rule := ApplyProfile(mustRule(t, constants.MoistureFiberSaturation), mustProfile(t, "pine", 30))
	pairs := [4]model.MoistureReading{
		{MeasuredAt: now.Add(-12 * time.Hour), SamplePosition: "core", MoisturePct: 34, DryBulbC: 56, WetBulbC: 47},
		{MeasuredAt: now.Add(-12 * time.Hour), SamplePosition: "surface", MoisturePct: 33, DryBulbC: 56, WetBulbC: 47},
		{MeasuredAt: now.Add(-6 * time.Hour), SamplePosition: "core", MoisturePct: 33, DryBulbC: 56, WetBulbC: 47},
		{MeasuredAt: now.Add(-6 * time.Hour), SamplePosition: "surface", MoisturePct: 32, DryBulbC: 56, WetBulbC: 47},
	}
	return lot, kiln, rule, pairs[0], pairs[1], pairs[2], pairs[3], now
}

func mustRule(t *testing.T, stage string) Rule {
	t.Helper()
	rule, ok := RuleFor(stage)
	if !ok {
		t.Fatalf("missing rule for stage %s", stage)
	}
	return rule
}

func mustProfile(t *testing.T, species string, thickness float64) SpeciesProfile {
	t.Helper()
	profile, err := ProfileFor(species, thickness)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	return profile
}

func TestAdaptivePlanBuildsThreeDatedCheckpointsWithin24Hours(t *testing.T) {
	lot, kiln, rule, c1, s1, c2, s2, now := pineFiberEnvelope(t)
	readings := []model.MoistureReading{c1, s1, c2, s2}
	result, err := Evaluate(lot, kiln, readings, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	plan := result.AdaptivePlan
	if plan == nil {
		t.Fatal("adaptive plan missing")
	}
	if plan.Mode != PlanModeAdaptive || len(plan.Checkpoints) != 3 {
		t.Fatalf("plan = %+v", plan)
	}
	tempCap := rule.MaxTemperature
	humidityFloor := rule.MinHumidity
	if kiln.MaxTemperatureC < tempCap {
		tempCap = kiln.MaxTemperatureC
	}
	if kiln.MinHumidityPct > humidityFloor {
		humidityFloor = kiln.MinHumidityPct
	}
	for index, checkpoint := range plan.Checkpoints {
		if checkpoint.ReviewAt.Before(now) || checkpoint.ReviewAt.After(now.Add(24*time.Hour+time.Minute)) {
			t.Fatalf("checkpoint %d outside 24h window: %s", index+1, checkpoint.ReviewAt)
		}
		if checkpoint.TargetDryBulbC > tempCap || checkpoint.TargetHumidityPct < humidityFloor {
			t.Fatalf("checkpoint %d outside tighter envelope: %+v", index+1, checkpoint)
		}
		if checkpoint.PredictedMoisturePct < lot.TargetMoisturePct {
			t.Fatalf("checkpoint %d predicts below target", index+1)
		}
		if checkpoint.Action != CheckpointActionFollow {
			t.Fatalf("checkpoint %d action = %s", index+1, checkpoint.Action)
		}
	}
	last := plan.Checkpoints[2]
	if !last.ReviewAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("third checkpoint must close the 24h window: %s", last.ReviewAt)
	}
	// First checkpoint follows the observed 6-hour paired-reading cadence.
	if plan.Checkpoints[0].IntervalHours != 6 {
		t.Fatalf("first interval = %v, want 6", plan.Checkpoints[0].IntervalHours)
	}
}

func TestAdaptivePlanEnvelopeUsesTighterOfMaterialAndKiln(t *testing.T) {
	lot, kiln, rule, c1, s1, c2, s2, now := pineFiberEnvelope(t)
	// Kiln is tighter than the pine fiber rule on both axes.
	kiln.MaxTemperatureC, kiln.MinHumidityPct = 60, 58
	readings := []model.MoistureReading{c1, s1, c2, s2}
	result, err := Evaluate(lot, kiln, readings, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	for _, limit := range result.AdaptivePlan.EffectiveEnvelope {
		if limit.Parameter == "temperature" {
			if limit.EffectiveLimit != 60 || limit.ConstrainedBy != "kiln" {
				t.Fatalf("temperature envelope = %+v", limit)
			}
		}
		if limit.Parameter == "relative_humidity" {
			if limit.EffectiveLimit != 58 || limit.ConstrainedBy != "kiln" {
				t.Fatalf("humidity envelope = %+v", limit)
			}
		}
		if limit.Parameter == "moisture_gradient" {
			if limit.EffectiveLimit != rule.MaxGradient || limit.ConstrainedBy != "material" {
				t.Fatalf("gradient envelope = %+v", limit)
			}
		}
	}
	for _, checkpoint := range result.AdaptivePlan.Checkpoints {
		if checkpoint.TargetDryBulbC > 60 || checkpoint.TargetHumidityPct < 58 {
			t.Fatalf("checkpoint breaches tighter kiln boundary: %+v", checkpoint)
		}
	}
}

func TestFailedRulesForceEqualizingHoldWithoutHeatingOrDehumidifying(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lot := model.TimberLot{Species: "oak", ThicknessMM: 30, TargetMoisturePct: 10, InitialMoisturePct: 70, LoadedAt: now.Add(-3 * time.Hour)}
	kiln := model.DryingKiln{MaxTemperatureC: 72, MinHumidityPct: 32}
	readings := []model.MoistureReading{
		{MeasuredAt: now.Add(-2 * time.Hour), SamplePosition: "core", MoisturePct: 70, DryBulbC: 52, WetBulbC: 46},
		{MeasuredAt: now.Add(-time.Hour), SamplePosition: "core", MoisturePct: 40, DryBulbC: 52, WetBulbC: 46},
		{MeasuredAt: now.Add(-time.Hour), SamplePosition: "surface", MoisturePct: 38, DryBulbC: 52, WetBulbC: 46},
	}
	result, err := Evaluate(lot, kiln, readings, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	plan := result.AdaptivePlan
	if plan.Mode != PlanModeEqualizingHold || len(plan.HoldReasons) == 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Checkpoints) != 3 {
		t.Fatalf("checkpoints = %d", len(plan.Checkpoints))
	}
	for index, checkpoint := range plan.Checkpoints {
		if checkpoint.Action != CheckpointActionRecheck {
			t.Fatalf("checkpoint %d action = %s", index+1, checkpoint.Action)
		}
		// Relative to the current 52C / 70% RH cabin the hold may only cool or
		// humidify back into the envelope; never heat up or dry down.
		if checkpoint.TargetDryBulbC > plan.CurrentDryBulbC {
			t.Fatalf("checkpoint %d raises temperature: %+v", index+1, checkpoint)
		}
		if checkpoint.TargetHumidityPct < plan.CurrentHumidity {
			t.Fatalf("checkpoint %d lowers humidity: %+v", index+1, checkpoint)
		}
		if checkpoint.PredictedMoisturePct != plan.CurrentMoisture {
			t.Fatalf("hold must not extrapolate moisture loss: %+v", checkpoint)
		}
	}
}

func TestWideCheckpointGapRequestsInterimPairedMeasurement(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lot := model.TimberLot{Species: "pine", ThicknessMM: 30, TargetMoisturePct: 10, InitialMoisturePct: 28, LoadedAt: now.Add(-4 * time.Hour)}
	kiln := model.DryingKiln{MaxTemperatureC: 70, MinHumidityPct: 30}
	readings := []model.MoistureReading{
		{MeasuredAt: now.Add(-3 * time.Hour), SamplePosition: "core", MoisturePct: 28, DryBulbC: 52, WetBulbC: 44},
		{MeasuredAt: now.Add(-3 * time.Hour), SamplePosition: "surface", MoisturePct: 27, DryBulbC: 52, WetBulbC: 44},
		{MeasuredAt: now.Add(-2 * time.Hour), SamplePosition: "core", MoisturePct: 27, DryBulbC: 52, WetBulbC: 44},
		{MeasuredAt: now.Add(-2 * time.Hour), SamplePosition: "surface", MoisturePct: 26, DryBulbC: 52, WetBulbC: 44},
	}
	plan := BuildAdaptivePlan(lot, kiln,
		ApplyProfile(mustRule(t, constants.MoistureTarget), mustProfile(t, "pine", 30)),
		mustProfile(t, "pine", 30), readings, 52, 60, 26.5, 0.4, nil, now)
	if plan.Mode != PlanModeAdaptive {
		t.Fatalf("mode = %s", plan.Mode)
	}
	reminders := 0
	for _, checkpoint := range plan.Checkpoints {
		if checkpoint.InterimReminder {
			reminders++
			if checkpoint.InterimReminderAt == nil {
				t.Fatalf("reminder without time: %+v", checkpoint)
			}
			if checkpoint.IntervalHours <= interimReminderGapHours {
				t.Fatalf("reminder on short gap: %+v", checkpoint)
			}
		}
	}
	if reminders == 0 || len(plan.Warnings) == 0 {
		t.Fatalf("expected interim reminder: %+v", plan)
	}
}

func TestMissingPairedReadingsFallsBackToStageCadence(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lot := model.TimberLot{Species: "pine", ThicknessMM: 30, TargetMoisturePct: 10, InitialMoisturePct: 30, LoadedAt: now.Add(-2 * time.Hour)}
	kiln := model.DryingKiln{MaxTemperatureC: 70, MinHumidityPct: 30}
	// Only one paired event cannot establish a two-event cadence, but the pair
	// itself keeps sample-pair coverage at 100%, so the plan stays adaptive.
	readings := []model.MoistureReading{
		{MeasuredAt: now, SamplePosition: "core", MoisturePct: 30, DryBulbC: 50, WetBulbC: 42},
		{MeasuredAt: now, SamplePosition: "surface", MoisturePct: 29, DryBulbC: 50, WetBulbC: 42},
	}
	result, err := Evaluate(lot, kiln, readings, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	plan := result.AdaptivePlan
	if plan.Mode != PlanModeAdaptive || plan.PairedEventCount != 1 || plan.CadenceHours != 8 {
		t.Fatalf("cadence fallback = %+v", plan)
	}
	if len(plan.Warnings) == 0 {
		t.Fatal("expected warning asking for two paired readings")
	}
}

func TestUnpairedSingleEventForcesEqualizingRecheck(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lot := model.TimberLot{Species: "pine", ThicknessMM: 30, TargetMoisturePct: 10, InitialMoisturePct: 30, LoadedAt: now.Add(-time.Hour)}
	kiln := model.DryingKiln{MaxTemperatureC: 70, MinHumidityPct: 30}
	readings := []model.MoistureReading{{MeasuredAt: now, SamplePosition: "core", MoisturePct: 30, DryBulbC: 50, WetBulbC: 42}}
	result, err := Evaluate(lot, kiln, readings, now)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.AdaptivePlan.Mode != PlanModeEqualizingHold {
		t.Fatalf("mode = %s", result.AdaptivePlan.Mode)
	}
	found := false
	for _, reason := range result.AdaptivePlan.HoldReasons {
		if reason == "sample_pair_coverage" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hold reasons = %v", result.AdaptivePlan.HoldReasons)
	}
}
