package algorithm

import (
	"fmt"
	"math"
	"sort"
	"time"

	"timber-kiln-drying-optimizer/backend/internal/model"
)

const (
	// PlanModeAdaptive issues small, envelope-bounded setpoint moves.
	PlanModeAdaptive = "adaptive"
	// PlanModeEqualizingRetest holds the kiln and schedules re-measurement; no
	// heating or dehumidification move is permitted while a rule is failing.
	PlanModeEqualizingRetest = "equalizing_retest"

	// SupplementalGapHours triggers a midpoint paired re-measurement reminder.
	SupplementalGapHours = 8.0
)

// PlanEnvelope is the intersection of the material stage rule and the kiln's
// own hard boundary: the smaller temperature/range and larger humidity floor.
type PlanEnvelope struct {
	MaxTemperature        float64 `json:"max_temperature"`
	MinHumidity           float64 `json:"min_humidity"`
	MaxGradient           float64 `json:"max_gradient"`
	MaxDryingRate         float64 `json:"max_drying_rate"`
	TempConstrainedBy     string  `json:"temp_constrained_by"`
	HumidityConstrainedBy string  `json:"humidity_constrained_by"`
}

// Checkpoint is one adaptive re-measurement point for the drying crew.
type Checkpoint struct {
	Index             int       `json:"index"`
	ReviewAt          time.Time `json:"review_at"`
	IntervalHours     float64   `json:"interval_hours"`
	TargetDryBulbC    float64   `json:"target_dry_bulb_c"`
	TargetHumidityPct float64   `json:"target_humidity_pct"`
	ExpectedMoisture  float64   `json:"expected_moisture_pct"`
	SupplementalTest  bool      `json:"supplemental_test"`
	Note              string    `json:"note"`
}

// AdaptivePlan is the per-simulation 24h operating schedule.
type AdaptivePlan struct {
	Mode                 string       `json:"mode"`
	Stage                string       `json:"stage"`
	Envelope             PlanEnvelope `json:"envelope"`
	Checkpoints          []Checkpoint `json:"checkpoints"`
	Warnings             []string     `json:"warnings"`
	PairEventsUsed       int          `json:"pair_events_used"`
	ObservedCadenceHours float64      `json:"observed_cadence_hours"`
	FailedConstraints    []string     `json:"failed_constraints"`
}

type pairedEvent struct {
	at       time.Time
	moisture float64
}

// buildAdaptivePlan turns a single static recommendation into three adaptive
// checkpoints derived from the current stage and the two most recent paired
// core/surface readings. Any failing evidence forces equalizing retests with
// no temperature rise and no humidity drop.
func buildAdaptivePlan(lot model.TimberLot, kiln model.DryingKiln, readings []model.MoistureReading, rule Rule, profile SpeciesProfile, stage string, currentTemp, currentHumidity, avgMoisture float64, evidence []RuleEvidence, baseTime time.Time, coverage SeriesCoverage) AdaptivePlan {
	envelope := effectiveEnvelope(kiln, rule)

	failedConstraints := []string{}
	for _, item := range evidence {
		if !item.Passed {
			failedConstraints = append(failedConstraints, item.Constraint)
		}
	}
	mode := PlanModeAdaptive
	if len(failedConstraints) > 0 {
		mode = PlanModeEqualizingRetest
	}

	paired := latestPairedEvents(readings)
	currentMoisture := avgMoisture
	if len(paired) > 0 {
		currentMoisture = paired[len(paired)-1].moisture
	}

	cadence, observedGap, cadenceWarning := planCadence(mode, stage, profile, paired)
	warnings := append([]string{}, coverage.Warnings...)
	warnings = append(warnings, cadenceWarning...)
	if mode == PlanModeEqualizingRetest {
		warnings = append(warnings, fmt.Sprintf("未通过规则：%s。先安排均衡复测，复测合格前不得升温或降湿。", joinCN(failedConstraints, "、")))
	}

	targetTemp, targetHumidity := currentTemp, currentHumidity
	if mode == PlanModeAdaptive {
		if aspirTemp, aspirHumidity, ok := chooseAdaptiveTargets(lot, envelope, currentTemp, currentHumidity, currentMoisture); ok {
			targetTemp, targetHumidity = aspirTemp, aspirHumidity
		}
	} else {
		// Holding pattern: never raise temperature, never lower humidity. If the
		// crew is already outside the envelope the safe direction is back to it.
		targetTemp = math.Min(currentTemp, envelope.MaxTemperature)
		targetHumidity = math.Max(currentHumidity, envelope.MinHumidity)
	}

	projectedRate := 0.0
	if mode == PlanModeAdaptive {
		projectedRate = projectedDryingRate(envelope, paired)
	}

	supplemental := observedGap > SupplementalGapHours
	checkpoints := make([]Checkpoint, 0, 3)
	for index := 1; index <= 3; index++ {
		cumulative := cadence * float64(index)
		reviewAt := baseTime.Add(time.Duration(math.Round(cumulative*60)) * time.Minute)
		expected := round(currentMoisture, 1)
		if mode == PlanModeAdaptive {
			expected = round(math.Max(lot.TargetMoisturePct, currentMoisture-projectedRate*cumulative), 1)
			dryTemp := moveToward(checkpointTemp(checkpoints, currentTemp), targetTemp, rule.MaxTemperatureStep, 0, envelope.MaxTemperature)
			humidity := moveToward(checkpointHumidity(checkpoints, currentHumidity), targetHumidity, rule.MaxHumidityStep, envelope.MinHumidity, 100)
			targetTemp, targetHumidity = dryTemp, humidity
		}
		note := "按当前阶段与最近成对读数自适应推进，复查温度、湿度与成对含水率。"
		if mode == PlanModeEqualizingRetest {
			note = "均衡保持后复测；复测合格前保持当前干球温度、不主动降湿。"
		}
		if supplemental {
			note = fmt.Sprintf("距上一测点 %.1fh（超过 8h），请在间隔中点补测一次中心/表层成对读数。", cadence)
		}
		checkpoints = append(checkpoints, Checkpoint{
			Index:             index,
			ReviewAt:          reviewAt,
			IntervalHours:     round(cadence, 1),
			TargetDryBulbC:    round(targetTemp, 1),
			TargetHumidityPct: round(targetHumidity, 1),
			ExpectedMoisture:  expected,
			SupplementalTest:  supplemental,
			Note:              note,
		})
	}

	return AdaptivePlan{
		Mode:                 mode,
		Stage:                stage,
		Envelope:             envelope,
		Checkpoints:          checkpoints,
		Warnings:             warnings,
		PairEventsUsed:       len(paired),
		ObservedCadenceHours: round(cadence, 1),
		FailedConstraints:    failedConstraints,
	}
}

func effectiveEnvelope(kiln model.DryingKiln, rule Rule) PlanEnvelope {
	envelope := PlanEnvelope{
		MaxTemperature:        rule.MaxTemperature,
		MinHumidity:           rule.MinHumidity,
		MaxGradient:           rule.MaxGradient,
		MaxDryingRate:         rule.MaxDryingRate,
		TempConstrainedBy:     "material",
		HumidityConstrainedBy: "material",
	}
	if kiln.MaxTemperatureC < rule.MaxTemperature {
		envelope.MaxTemperature = kiln.MaxTemperatureC
		envelope.TempConstrainedBy = "kiln"
	}
	if kiln.MinHumidityPct > rule.MinHumidity {
		envelope.MinHumidity = kiln.MinHumidityPct
		envelope.HumidityConstrainedBy = "kiln"
	}
	return envelope
}

// latestPairedEvents returns sampling events carrying both a core and a
// surface sample, newest last. Only the trailing pair is needed for cadence,
// the full tail is returned for rate projection.
func latestPairedEvents(readings []model.MoistureReading) []pairedEvent {
	grouped := map[time.Time][]model.MoistureReading{}
	times := []time.Time{}
	for _, item := range readings {
		if _, seen := grouped[item.MeasuredAt]; !seen {
			times = append(times, item.MeasuredAt)
		}
		grouped[item.MeasuredAt] = append(grouped[item.MeasuredAt], item)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	events := []pairedEvent{}
	for _, at := range times {
		rows := grouped[at]
		core, surface, values := []float64{}, []float64{}, []float64{}
		for _, row := range rows {
			values = append(values, row.MoisturePct)
			switch {
			case isCorePosition(row.SamplePosition):
				core = append(core, row.MoisturePct)
			case isSurfacePosition(row.SamplePosition):
				surface = append(surface, row.MoisturePct)
			}
		}
		if len(core) == 0 || len(surface) == 0 {
			continue
		}
		events = append(events, pairedEvent{at: at, moisture: mean(values)})
	}
	if len(events) <= 2 {
		return events
	}
	return events[len(events)-2:]
}

func isCorePosition(position string) bool {
	switch position {
	case "core", "center", "中心":
		return true
	}
	return false
}
func isSurfacePosition(position string) bool {
	return position == "surface" || position == "表层"
}

// planCadence derives checkpoint spacing from the gap between the two most
// recent paired events, falling back to a stage default, and to the material
// equalizing window during a retest.
func planCadence(mode, stage string, profile SpeciesProfile, paired []pairedEvent) (cadence, observedGap float64, warnings []string) {
	if mode == PlanModeEqualizingRetest {
		return clamp(profile.EqualizingHours, 2, SupplementalGapHours), 0, nil
	}
	stageDefault := map[string]float64{
		"green":            8,
		"fiber_saturation": 8,
		"bound_water":      6,
		"target":           4,
	}[stage]
	if len(paired) < 2 {
		warnings = append(warnings, fmt.Sprintf("最近两次成对中心/表层读数不足，检查点间隔采用 %s 阶段默认 %.0fh。", stage, stageDefault))
		return stageDefault, 0, warnings
	}
	observedGap = paired[len(paired)-1].at.Sub(paired[len(paired)-2].at).Hours()
	cadence = clamp(observedGap, 4, 12)
	if observedGap > SupplementalGapHours {
		warnings = append(warnings, fmt.Sprintf("最近成对读数间隔 %.1fh 超过 8h，检查点之间需安排补测。", observedGap))
	}
	return cadence, observedGap, warnings
}

func projectedDryingRate(envelope PlanEnvelope, paired []pairedEvent) float64 {
	if len(paired) >= 2 {
		gap := paired[len(paired)-1].at.Sub(paired[len(paired)-2].at).Hours()
		if gap > 0 {
			rate := (paired[len(paired)-2].moisture - paired[len(paired)-1].moisture) / gap
			if rate > 0 {
				return math.Min(rate, envelope.MaxDryingRate)
			}
		}
	}
	// No trustworthy recent decline: project with a conservative third of the
	// stage/material rate ceiling so expected moisture never promises speed.
	return math.Min(envelope.MaxDryingRate*0.3, 0.15)
}

// chooseAdaptiveTargets mirrors the static suggestion candidate search so a
// plan always walks toward the same best safe operating point.
func chooseAdaptiveTargets(lot model.TimberLot, envelope PlanEnvelope, currentTemp, currentHumidity, moisture float64) (float64, float64, bool) {
	type candidate struct{ temp, humidity, score float64 }
	candidates := []candidate{}
	for temp := math.Max(0, currentTemp-4); temp <= math.Min(envelope.MaxTemperature, currentTemp+4); temp += 1 {
		for humidity := math.Max(envelope.MinHumidity, currentHumidity-8); humidity <= math.Min(100, currentHumidity+8); humidity += 2 {
			if temp > envelope.MaxTemperature || humidity < envelope.MinHumidity {
				continue
			}
			dryNeed := math.Max(0, moisture-lot.TargetMoisturePct)
			score := temp*dryNeed/100 - math.Abs(temp-currentTemp)*0.4 - math.Abs(humidity-currentHumidity)*0.08
			candidates = append(candidates, candidate{temp, humidity, score})
		}
	}
	if len(candidates) == 0 {
		return currentTemp, currentHumidity, false
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	return round(candidates[0].temp, 1), round(candidates[0].humidity, 1), true
}

func moveToward(current, target, step, lower, upper float64) float64 {
	next := current
	switch {
	case target > current:
		next = math.Min(current+step, target)
	case target < current:
		next = math.Max(current-step, target)
	}
	return math.Min(upper, math.Max(lower, next))
}

func checkpointTemp(points []Checkpoint, fallback float64) float64 {
	if len(points) == 0 {
		return fallback
	}
	return points[len(points)-1].TargetDryBulbC
}
func checkpointHumidity(points []Checkpoint, fallback float64) float64 {
	if len(points) == 0 {
		return fallback
	}
	return points[len(points)-1].TargetHumidityPct
}

func clamp(value, lower, upper float64) float64 {
	return math.Min(upper, math.Max(lower, value))
}

func joinCN(items []string, sep string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += sep
		}
		out += item
	}
	return out
}
