package algorithm

import (
	"testing"
	"time"

	"timber-kiln-drying-optimizer/backend/internal/constants"
	"timber-kiln-drying-optimizer/backend/internal/model"
)

func adaptiveFixture(t *testing.T, now time.Time) (model.TimberLot, model.DryingKiln, []model.MoistureReading) {
	t.Helper()
	lot := model.TimberLot{Species: "pine", ThicknessMM: 30, TargetMoisturePct: 10, InitialMoisturePct: 30, LoadedAt: now.Add(-20 * time.Hour)}
	kiln := model.DryingKiln{MaxTemperatureC: 72, MinHumidityPct: 32}
	readings := []model.MoistureReading{
		{MeasuredAt: now.Add(-8 * time.Hour), SamplePosition: "core", MoisturePct: 30.4, DryBulbC: 50, WetBulbC: 42},
		{MeasuredAt: now.Add(-8 * time.Hour), SamplePosition: "surface", MoisturePct: 29.6, DryBulbC: 50, WetBulbC: 42},
		{MeasuredAt: now.Add(-4 * time.Hour), SamplePosition: "core", MoisturePct: 29.6, DryBulbC: 50, WetBulbC: 42},
		{MeasuredAt: now.Add(-4 * time.Hour), SamplePosition: "surface", MoisturePct: 28.8, DryBulbC: 50, WetBulbC: 42},
	}
	return lot, kiln, readings
}

func TestAdaptivePlanSchedulesThreeCheckpointsFromPairedCadence(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	lot, kiln, readings := adaptiveFixture(t, now)
	result, err := Evaluate(lot, kiln, readings, now)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	plan := result.Plan
	if plan.Mode != PlanModeAdaptive || plan.Stage != constants.MoistureFiberSaturation {
		t.Fatalf("unexpected plan mode/stage: %+v", plan)
	}
	if len(plan.Checkpoints) != 3 {
		t.Fatalf("expected 3 checkpoints, got %d", len(plan.Checkpoints))
	}
	for index, checkpoint := range plan.Checkpoints {
		if checkpoint.Index != index+1 {
			t.Fatalf("checkpoint index = %d, want %d", checkpoint.Index, index+1)
		}
		wantInterval := 4.0 * float64(index+1)
		wantReview := now.Add(-4 * time.Hour).Add(time.Duration(wantInterval) * time.Hour)
		if checkpoint.ReviewAt != wantReview {
			t.Fatalf("checkpoint %d review at %s, want %s", index+1, checkpoint.ReviewAt, wantReview)
		}
		if checkpoint.IntervalHours != 4 || checkpoint.SupplementalTest {
			t.Fatalf("checkpoint %d should follow the 6h paired cadence without supplemental testing: %+v", index+1, checkpoint)
		}
		if checkpoint.ExpectedMoisture < lot.TargetMoisturePct {
			t.Fatalf("expected moisture must not cross target: %+v", checkpoint)
		}
	}
	last := plan.Checkpoints[2]
	if last.ExpectedMoisture >= plan.Checkpoints[0].ExpectedMoisture {
		t.Fatalf("expected moisture must decline across adaptive checkpoints: %+v", plan.Checkpoints)
	}
}

func TestAdaptivePlanUsesSmallerEnvelopeOfRuleAndKiln(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	lot := model.TimberLot{Species: "oak", ThicknessMM: 30, TargetMoisturePct: 10, InitialMoisturePct: 48, LoadedAt: now.Add(-20 * time.Hour)}
	kiln := model.DryingKiln{MaxTemperatureC: 45, MinHumidityPct: 80}
	readings := []model.MoistureReading{
		{MeasuredAt: now.Add(-12 * time.Hour), SamplePosition: "core", MoisturePct: 46, DryBulbC: 42, WetBulbC: 40},
		{MeasuredAt: now.Add(-12 * time.Hour), SamplePosition: "surface", MoisturePct: 45, DryBulbC: 42, WetBulbC: 40},
		{MeasuredAt: now.Add(-6 * time.Hour), SamplePosition: "core", MoisturePct: 44.5, DryBulbC: 42, WetBulbC: 40},
		{MeasuredAt: now.Add(-6 * time.Hour), SamplePosition: "surface", MoisturePct: 43.5, DryBulbC: 42, WetBulbC: 40},
	}
	result, err := Evaluate(lot, kiln, readings, now)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	envelope := result.Plan.Envelope
	if envelope.MaxTemperature != 45 || envelope.TempConstrainedBy != "kiln" {
		t.Fatalf("temperature envelope should be the smaller kiln bound: %+v", envelope)
	}
	if envelope.MinHumidity != 80 || envelope.HumidityConstrainedBy != "kiln" {
		t.Fatalf("humidity envelope should be the larger kiln floor: %+v", envelope)
	}
	for _, checkpoint := range result.Plan.Checkpoints {
		if checkpoint.TargetDryBulbC > envelope.MaxTemperature {
			t.Fatalf("checkpoint temperature exceeds envelope: %+v", checkpoint)
		}
		if checkpoint.TargetHumidityPct < envelope.MinHumidity {
			t.Fatalf("checkpoint humidity drops below envelope: %+v", checkpoint)
		}
	}
}

func TestAdaptivePlanFlagsSupplementalTestingBeyondEightHours(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	lot := model.TimberLot{Species: "pine", ThicknessMM: 30, TargetMoisturePct: 10, InitialMoisturePct: 30, LoadedAt: now.Add(-30 * time.Hour)}
	kiln := model.DryingKiln{MaxTemperatureC: 72, MinHumidityPct: 32}
	readings := []model.MoistureReading{
		{MeasuredAt: now.Add(-24 * time.Hour), SamplePosition: "core", MoisturePct: 31, DryBulbC: 50, WetBulbC: 42},
		{MeasuredAt: now.Add(-24 * time.Hour), SamplePosition: "surface", MoisturePct: 30, DryBulbC: 50, WetBulbC: 42},
		{MeasuredAt: now.Add(-12 * time.Hour), SamplePosition: "core", MoisturePct: 29, DryBulbC: 50, WetBulbC: 42},
		{MeasuredAt: now.Add(-12 * time.Hour), SamplePosition: "surface", MoisturePct: 28, DryBulbC: 50, WetBulbC: 42},
	}
	result, err := Evaluate(lot, kiln, readings, now)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	plan := result.Plan
	if plan.Mode != PlanModeAdaptive {
		t.Fatalf("plan should stay adaptive: %+v", plan)
	}
	for _, checkpoint := range plan.Checkpoints {
		if !checkpoint.SupplementalTest {
			t.Fatalf("12h cadence requires supplemental testing at every checkpoint: %+v", checkpoint)
		}
	}
	if !containsWarning(plan.Warnings, "补测") {
		t.Fatalf("plan warnings must mention supplemental testing: %+v", plan.Warnings)
	}
}

func TestEqualizingRetestForbidsHeatingAndDehumidification(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	lot := model.TimberLot{Species: "oak", ThicknessMM: 30, TargetMoisturePct: 10, InitialMoisturePct: 70, LoadedAt: now.Add(-3 * time.Hour)}
	kiln := model.DryingKiln{MaxTemperatureC: 72, MinHumidityPct: 32}
	readings := []model.MoistureReading{
		{MeasuredAt: now.Add(-2 * time.Hour), SamplePosition: "core", MoisturePct: 70, DryBulbC: 52, WetBulbC: 46},
		{MeasuredAt: now.Add(-time.Hour), SamplePosition: "core", MoisturePct: 40, DryBulbC: 52, WetBulbC: 46},
		{MeasuredAt: now.Add(-time.Hour), SamplePosition: "surface", MoisturePct: 38, DryBulbC: 52, WetBulbC: 46},
	}
	result, err := Evaluate(lot, kiln, readings, now)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	plan := result.Plan
	if plan.Mode != PlanModeEqualizingRetest {
		t.Fatalf("failing rate evidence must force equalizing retest: %+v", plan)
	}
	if len(plan.FailedConstraints) == 0 || len(plan.Checkpoints) != 3 {
		t.Fatalf("retest plan must carry failed constraints and 3 checkpoints: %+v", plan)
	}
	currentTemp, currentHumidity := 52.0, 70.0
	for _, checkpoint := range plan.Checkpoints {
		if checkpoint.TargetDryBulbC > currentTemp {
			t.Fatalf("retest checkpoint must not raise temperature: %+v", checkpoint)
		}
		if checkpoint.TargetHumidityPct < currentHumidity {
			t.Fatalf("retest checkpoint must not lower humidity: %+v", checkpoint)
		}
		if checkpoint.ExpectedMoisture != 39.0 {
			t.Fatalf("retest must hold the latest paired moisture rather than project drying: %+v", checkpoint)
		}
	}
}

func TestAdaptivePlanIsDeterministicForIdenticalInput(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	lot, kiln, readings := adaptiveFixture(t, now)
	first, err := Evaluate(lot, kiln, readings, now)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	second, err := Evaluate(lot, kiln, readings, now)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	for index := range first.Plan.Checkpoints {
		a, b := first.Plan.Checkpoints[index], second.Plan.Checkpoints[index]
		if a != b {
			t.Fatalf("checkpoint %d differs between identical runs: %+v vs %+v", index+1, a, b)
		}
	}
}

func containsWarning(warnings []string, fragment string) bool {
	for _, warning := range warnings {
		if len(warning) > 0 && contains(warning, fragment) {
			return true
		}
	}
	return false
}

func contains(value, fragment string) bool {
	for i := 0; i+len(fragment) <= len(value); i++ {
		if value[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
