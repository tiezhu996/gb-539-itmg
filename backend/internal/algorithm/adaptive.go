package algorithm

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"timber-kiln-drying-optimizer/backend/internal/constants"
	"timber-kiln-drying-optimizer/backend/internal/model"
)

const (
	PlanModeAdaptive        = "adaptive"
	PlanModeEqualizingHold  = "equalizing_hold"
	CheckpointActionFollow  = "adaptive_follow"
	CheckpointActionRecheck = "equalizing_recheck"
	adaptiveWindowHours     = 24
	interimReminderGapHours = 8
)

// Checkpoint is one shop-floor re-check inside the next 24 hours. Every value
// is advisory and stays inside the intersection of material rule and kiln
// boundaries; no setpoint is ever sent to equipment.
type Checkpoint struct {
	Sequence             int        `json:"sequence"`
	Action               string     `json:"action"`
	ReviewAt             time.Time  `json:"review_at"`
	IntervalHours        float64    `json:"interval_hours"`
	TargetDryBulbC       float64    `json:"target_dry_bulb_c"`
	TargetHumidityPct    float64    `json:"target_humidity_pct"`
	PredictedMoisturePct float64    `json:"predicted_moisture_pct"`
	InterimReminder      bool       `json:"interim_reminder"`
	InterimReminderAt    *time.Time `json:"interim_reminder_at,omitempty"`
	Note                 string     `json:"note"`
}

// EffectiveEnvelope reports one effective limit. Temperature and humidity use
// the tighter of material rule and kiln boundary; gradient and drying rate are
// bounded by the material rule only because kilns carry no such hard limit.
type EffectiveEnvelope struct {
	Parameter      string  `json:"parameter"`
	MaterialLimit  float64 `json:"material_limit"`
	KilnLimit      float64 `json:"kiln_limit"`
	EffectiveLimit float64 `json:"effective_limit"`
	ConstrainedBy  string  `json:"constrained_by"`
}

// AdaptivePlan turns one evaluation into three dated checkpoints covering the
// next 24 hours. When any current rule fails it becomes an equalizing hold:
// checkpoints only re-test, never raise temperature or lower humidity.
type AdaptivePlan struct {
	Mode              string              `json:"mode"`
	Stage             string              `json:"stage"`
	RuleID            string              `json:"rule_id"`
	RuleVersion       string              `json:"rule_version"`
	ProfileID         string              `json:"profile_id"`
	GeneratedAt       time.Time           `json:"generated_at"`
	WindowUntil       time.Time           `json:"window_until"`
	CurrentDryBulbC   float64             `json:"current_dry_bulb_c"`
	CurrentHumidity   float64             `json:"current_humidity_pct"`
	CurrentMoisture   float64             `json:"current_moisture_pct"`
	TargetMoisture    float64             `json:"target_moisture_pct"`
	CadenceHours      float64             `json:"cadence_hours"`
	PairedEventCount  int                 `json:"paired_event_count"`
	Checkpoints       []Checkpoint        `json:"checkpoints"`
	Warnings          []string            `json:"warnings"`
	EffectiveEnvelope []EffectiveEnvelope `json:"effective_envelope"`
	HoldReasons       []string            `json:"hold_reasons,omitempty"`
	Basis             string              `json:"basis"`
}

type pairedEvent struct {
	at       time.Time
	moisture float64
}

// defaultCadenceHours is the stage-specific re-check rhythm used when fewer
// than two paired sampling events are available. Every default stays at or
// under 8 hours so a normal plan never needs an extra interim measurement.
func defaultCadenceHours(stage string) float64 {
	switch stage {
	case constants.MoistureGreen:
		return 8
	case constants.MoistureFiberSaturation:
		return 8
	case constants.MoistureBoundWater:
		return 6
	case constants.MoistureTarget:
		return 4
	default:
		return 8
	}
}

func pairedEvents(readings []model.MoistureReading) []pairedEvent {
	grouped := map[time.Time]map[string][]float64{}
	times := []time.Time{}
	for _, item := range readings {
		position := strings.ToLower(item.SamplePosition)
		if position != "core" && position != "center" && position != "中心" && position != "surface" && position != "表层" {
			continue
		}
		if grouped[item.MeasuredAt] == nil {
			grouped[item.MeasuredAt] = map[string][]float64{"core": {}, "surface": {}}
			times = append(times, item.MeasuredAt)
		}
		key := "surface"
		if position == "core" || position == "center" || position == "中心" {
			key = "core"
		}
		grouped[item.MeasuredAt][key] = append(grouped[item.MeasuredAt][key], item.MoisturePct)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	events := []pairedEvent{}
	for _, at := range times {
		pairs := grouped[at]
		if len(pairs["core"]) == 0 || len(pairs["surface"]) == 0 {
			continue
		}
		values := append(append([]float64{}, pairs["core"]...), pairs["surface"]...)
		events = append(events, pairedEvent{at: at, moisture: mean(values)})
	}
	return events
}

// adaptiveCadence takes the smaller of the stage default and the gap between
// the two most recent paired readings, with a 4-hour shop-floor floor.
func adaptiveCadence(stage string, events []pairedEvent) (float64, string) {
	cadence := defaultCadenceHours(stage)
	basis := fmt.Sprintf("阶段默认 %.0f 小时", cadence)
	if len(events) >= 2 {
		gap := events[len(events)-1].at.Sub(events[len(events)-2].at).Hours()
		observed := round(gap, 1)
		cadence = math.Min(defaultCadenceHours(stage), math.Max(4, observed))
		basis = fmt.Sprintf("阶段默认 %.0f 小时与最近两次成对读数间隔 %.1f 小时的更小值", defaultCadenceHours(stage), observed)
	}
	return cadence, basis
}

func effectiveEnvelopeFor(kiln model.DryingKiln, rule Rule) []EffectiveEnvelope {
	temperature := EffectiveEnvelope{Parameter: "temperature", MaterialLimit: rule.MaxTemperature, KilnLimit: kiln.MaxTemperatureC, EffectiveLimit: rule.MaxTemperature, ConstrainedBy: "material"}
	if kiln.MaxTemperatureC < rule.MaxTemperature {
		temperature.EffectiveLimit, temperature.ConstrainedBy = kiln.MaxTemperatureC, "kiln"
	}
	humidity := EffectiveEnvelope{Parameter: "relative_humidity", MaterialLimit: rule.MinHumidity, KilnLimit: kiln.MinHumidityPct, EffectiveLimit: rule.MinHumidity, ConstrainedBy: "material"}
	if kiln.MinHumidityPct > rule.MinHumidity {
		humidity.EffectiveLimit, humidity.ConstrainedBy = kiln.MinHumidityPct, "kiln"
	}
	return []EffectiveEnvelope{
		temperature,
		humidity,
		{Parameter: "moisture_gradient", MaterialLimit: rule.MaxGradient, KilnLimit: 0, EffectiveLimit: rule.MaxGradient, ConstrainedBy: "material"},
		{Parameter: "drying_rate_pct_per_hour", MaterialLimit: rule.MaxDryingRate, KilnLimit: 0, EffectiveLimit: rule.MaxDryingRate, ConstrainedBy: "material"},
	}
}

// BuildAdaptivePlan assembles the dated checkpoint list. The hold branch is the
// only legal answer when current evidence contains a failing rule: temperature
// can only stay or move down into the envelope and humidity can only stay or
// move up, so no checkpoint ever raises temperature or lowers humidity.
func BuildAdaptivePlan(lot model.TimberLot, kiln model.DryingKiln, rule Rule, profile SpeciesProfile, readings []model.MoistureReading, currentTemp, currentHumidity, avg, rate float64, evidence []RuleEvidence, evaluatedAt time.Time) AdaptivePlan {
	generatedAt := evaluatedAt.UTC()
	windowUntil := generatedAt.Add(adaptiveWindowHours * time.Hour)
	tempCap := math.Min(rule.MaxTemperature, kiln.MaxTemperatureC)
	humidityFloor := math.Max(rule.MinHumidity, kiln.MinHumidityPct)
	events := pairedEvents(readings)
	failed := []string{}
	for _, item := range evidence {
		if !item.Passed {
			failed = append(failed, item.Constraint)
		}
	}
	plan := AdaptivePlan{
		Stage:             rule.Stage,
		RuleID:            rule.ID,
		RuleVersion:       rule.Version,
		ProfileID:         profile.ID,
		GeneratedAt:       generatedAt,
		WindowUntil:       windowUntil,
		CurrentDryBulbC:   round(currentTemp, 1),
		CurrentHumidity:   round(currentHumidity, 1),
		CurrentMoisture:   round(avg, 1),
		TargetMoisture:    lot.TargetMoisturePct,
		PairedEventCount:  len(events),
		Warnings:          []string{},
		EffectiveEnvelope: effectiveEnvelopeFor(kiln, rule),
	}

	if len(failed) > 0 {
		plan.Mode = PlanModeEqualizingHold
		plan.HoldReasons = failed
		plan.CadenceHours = round(math.Max(2, profile.EqualizingHours), 1)
		plan.Checkpoints, plan.Warnings = buildHoldCheckpoints(generatedAt, windowUntil, plan.CadenceHours, currentTemp, currentHumidity, tempCap, humidityFloor, avg)
		plan.Basis = fmt.Sprintf("当前规则未全部通过（%s），先排均衡复测：三个检查点均保持/回到有效安全包线（干球不高于 %.1fC、相对湿度不低于 %.1f%%），不允许升温或降湿；复测合格后再重新仿真后续 24 小时。首次复查按 %s 材料均衡时长 %.0f 小时安排。", strings.Join(failed, "、"), tempCap, humidityFloor, profile.ID, profile.EqualizingHours)
		return plan
	}

	plan.Mode = PlanModeAdaptive
	cadence, cadenceBasis := adaptiveCadence(rule.Stage, events)
	plan.CadenceHours = cadence
	plan.Checkpoints, plan.Warnings = buildAdaptiveCheckpoints(generatedAt, windowUntil, cadence, currentTemp, currentHumidity, tempCap, humidityFloor, avg, lot.TargetMoisturePct, rate, rule)
	if len(events) < 2 {
		plan.Warnings = append(plan.Warnings, "最近两次成对（中心/表层）读数不足，复查节奏按阶段默认值；下次检查请同时采集中心与表层成对读数。")
	}
	plan.Basis = fmt.Sprintf("自适应排程：%s 阶段（规则 %s/%s + 材料目录 %s），复查节奏%s；目标干球取材料规则与窑炉边界的更小值 %.1fC，相对湿度取更大值 %.1f%%；预计含水率按最近两次成对读数的干燥速率 %.3f%%/h（时间跨度不足 4 小时按 4 小时计）外推且不低于目标 %.1f%%。", rule.Stage, rule.ID, rule.Version, profile.ID, cadenceBasis, tempCap, humidityFloor, rate, lot.TargetMoisturePct)
	return plan
}

// checkpointTimes keeps the first review at one cadence after generation and
// always ends the third review exactly at the 24-hour window, so the crew gets
// three dated points without extending the planning horizon.
func checkpointTimes(generatedAt, windowUntil time.Time, firstIntervalHours float64) []time.Time {
	first := generatedAt.Add(time.Duration(round(firstIntervalHours, 1) * float64(time.Hour)))
	if !first.Before(windowUntil) {
		first = generatedAt.Add(adaptiveWindowHours * time.Hour / 3)
	}
	second := first.Add(windowUntil.Sub(first) / 2)
	return []time.Time{first, second, windowUntil}
}

func withInterimReminder(previous, review time.Time) (bool, *time.Time) {
	gap := review.Sub(previous).Hours()
	if gap > interimReminderGapHours {
		midpoint := previous.Add(review.Sub(previous) / 2)
		return true, &midpoint
	}
	return false, nil
}

func buildAdaptiveCheckpoints(generatedAt, windowUntil time.Time, cadence, currentTemp, currentHumidity, tempCap, humidityFloor, avg, target, rate float64, rule Rule) ([]Checkpoint, []string) {
	times := checkpointTimes(generatedAt, windowUntil, cadence)
	checkpoints := make([]Checkpoint, 0, 3)
	warnings := []string{}
	previous := generatedAt
	for index, reviewAt := range times {
		sequence := index + 1
		// Small, step-limited moves; temperature never rises above the tighter
		// cap and humidity never falls below the tighter floor.
		targetTemp := math.Min(tempCap, currentTemp+float64(sequence)*rule.MaxTemperatureStep)
		targetHumidity := math.Max(humidityFloor, currentHumidity-float64(sequence)*rule.MaxHumidityStep)
		hours := reviewAt.Sub(generatedAt).Hours()
		projected := math.Max(target, avg-rate*hours)
		reminder, reminderAt := withInterimReminder(previous, reviewAt)
		note := fmt.Sprintf("第 %d 个自适应检查点：单步升温不超过 %.1fC、降湿不超过 %.1f%%，按实测读数校准后继续。", sequence, rule.MaxTemperatureStep, rule.MaxHumidityStep)
		if sequence == 3 {
			note = "24 小时窗口终点复查：导入成对读数后重新仿真下一窗口。"
		}
		cp := Checkpoint{Sequence: sequence, Action: CheckpointActionFollow, ReviewAt: reviewAt.UTC(), IntervalHours: round(reviewAt.Sub(previous).Hours(), 1), TargetDryBulbC: round(targetTemp, 1), TargetHumidityPct: round(targetHumidity, 1), PredictedMoisturePct: round(projected, 1), InterimReminder: reminder, InterimReminderAt: reminderAt, Note: note}
		if reminder {
			warnings = append(warnings, fmt.Sprintf("检查点 %d 距上一检查点 %.1f 小时（超过 %d 小时），请在 %s 补测一组中心/表层成对读数。", sequence, cp.IntervalHours, interimReminderGapHours, reminderAt.UTC().Format("01-02 15:04")))
		}
		checkpoints = append(checkpoints, cp)
		previous = reviewAt
	}
	return checkpoints, warnings
}

func buildHoldCheckpoints(generatedAt, windowUntil time.Time, holdHours, currentTemp, currentHumidity, tempCap, humidityFloor, avg float64) ([]Checkpoint, []string) {
	times := checkpointTimes(generatedAt, windowUntil, holdHours)
	checkpoints := make([]Checkpoint, 0, 3)
	warnings := []string{}
	previous := generatedAt
	// Safe directions only: cool down to the cap and humidify up to the floor;
	// never a temperature rise or humidity drop relative to current conditions.
	holdTemp := math.Min(currentTemp, tempCap)
	holdHumidity := math.Max(currentHumidity, humidityFloor)
	for index, reviewAt := range times {
		sequence := index + 1
		reminder, reminderAt := withInterimReminder(previous, reviewAt)
		note := "均衡复测：保持当前工况，复测中心/表层成对读数；本检查点不允许升温或降湿。"
		if sequence == 1 {
			note = "首次均衡复测：只确认梯度与干燥速率是否回到规则内，不允许升温或降湿。"
		}
		cp := Checkpoint{Sequence: sequence, Action: CheckpointActionRecheck, ReviewAt: reviewAt.UTC(), IntervalHours: round(reviewAt.Sub(previous).Hours(), 1), TargetDryBulbC: round(holdTemp, 1), TargetHumidityPct: round(holdHumidity, 1), PredictedMoisturePct: round(avg, 1), InterimReminder: reminder, InterimReminderAt: reminderAt, Note: note}
		if reminder {
			warnings = append(warnings, fmt.Sprintf("检查点 %d 距上一检查点 %.1f 小时（超过 %d 小时），请在 %s 补测一组中心/表层成对读数。", sequence, cp.IntervalHours, interimReminderGapHours, reminderAt.UTC().Format("01-02 15:04")))
		}
		checkpoints = append(checkpoints, cp)
		previous = reviewAt
	}
	return checkpoints, warnings
}
