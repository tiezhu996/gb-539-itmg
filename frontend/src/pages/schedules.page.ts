import {Component, inject, signal} from '@angular/core';
import {NgFor, NgIf} from '@angular/common';
import {AppStore} from '../stores/app.store';
import {scheduleApi} from '../api/schedules';
import type {Checkpoint, DryingSchedule, FrozenPlanComparison, RuleEvidence, StageMetric, Suggestion} from '../types/entities';
import {formatDate, formatSignedHours} from '../utils/format';

@Component({
  standalone:true,
  imports:[NgFor,NgIf],
  template:`
  <div class="page-head"><div><p class="eyebrow">04 / SCHEDULES</p><h1>曲线优化</h1><p class="description">每次仿真生成三个自适应检查点，供干燥班组直接安排未来 24 小时操作。</p></div><button class="button primary" (click)="calculate()">运行一次仿真</button></div>
  <div class="schedule-banner"><div class="banner-mark">∿</div><div><strong>算法版本 curve-v2.1 · 自适应排程</strong><p>按当前阶段与最近两次成对中心/表层读数安排检查点；温度、湿度、梯度与干燥速率均取材料规则与窑炉边界的更小范围。相同输入复用原计划。</p></div></div>
  <p class="form-error">{{error}}</p>

  @if (detail(); as item) {
  <section class="workspace-panel" style="margin-bottom:24px">
    <div class="panel-head"><div><h2>本计划 {{item.id.slice(0,8)}} <span class="state" [class.good]="item.plan_mode==='adaptive'" [class.warm]="item.plan_mode==='equalizing_retest'">{{modeName(item.plan_mode)}}</span></h2><p>{{item.algorithm_version}} · v{{item.version}} · {{item.schedule_state}} · 计算于 {{formatDate(item.calculated_at)}}</p></div><span class="count" [class.risk]="true" [class.low]="item.defect_risk_score<50" [class.high]="item.defect_risk_score>=50">风险 {{item.defect_risk_score}} / 100</span></div>

    @if (item.frozen_comparison; as frozen) {
    <div class="insight-strip" style="margin-bottom:0">
      <div><span class="strip-label">本计划完成时间</span><strong style="font-size:18px">{{frozen.predicted_finish_at ? formatDate(frozen.predicted_finish_at) : '—'}}</strong><small>预计</small></div>
      <div><span class="strip-label">与冻结算完成时间差</span><strong style="font-size:18px">{{formatSignedHours(frozen.finish_delta_hours)}}</strong><small>负=提前</small></div>
      <div><span class="strip-label">风险差</span><strong style="font-size:18px" [class.risk]="true" [class.low]="frozen.risk_delta<=0" [class.high]="frozen.risk_delta>0">{{frozen.risk_delta>0?'+':''}}{{frozen.risk_delta}}</strong><small>冻结算风险 {{frozen.baseline_risk}}@if (frozen.frozen_at) { · 冻结 {{formatDate(frozen.frozen_at)}} }</small></div>
      <div class="strip-note">基线 {{frozen.baseline_schedule_id.slice(0,8)}} 完成 {{frozen.baseline_finish_at ? formatDate(frozen.baseline_finish_at) : '—'}}；冻结计划是不可变车间参照，本计划仍走既有审核与审计流程。</div>
    </div>
    }
  </section>

  @if (stages(item).length) {
  <section class="workspace-panel" style="margin-bottom:24px">
    <div class="panel-head"><div><h2>当前阶段与安全包络</h2><p>材料规则与窑炉边界取交集，温度取更低上限、湿度取更高下限。</p></div></div>
    <div class="table-wrap"><table><thead><tr><th>阶段</th><th>平均含水率</th><th>梯度</th><th>干燥速率 %/h</th><th>温度上限</th><th>湿度下限</th></tr></thead><tbody>
      <tr *ngFor="let stage of stages(item)"><td><strong>{{stageName(stage.stage)}}</strong></td><td>{{stage.average_moisture}}%</td><td>{{stage.gradient}}</td><td>{{stage.drying_rate}}</td><td>{{evidenceLimit(item,'temperature','min')}}°C</td><td>{{evidenceLimit(item,'relative_humidity','max')}}%</td></tr>
    </tbody></table></div>
  </section>
  }

  @if (failedEvidence(item).length) {
  <section class="workspace-panel" style="margin-bottom:24px;border-left:3px solid var(--copper)"><div class="panel-head"><div><h2>规则未通过 · 先均衡复测</h2><p>复测合格前计划不安排升温或降湿，仅保持并等待成对复测。</p></div></div>
    <div class="rule-list" style="margin:0 26px 24px"><div *ngFor="let evidence of failedEvidence(item)"><strong>{{constraintName(evidence.constraint)}}</strong><span>实测 {{evidence.observed}} · 限值 {{evidence.limit}} · {{evidence.rule_id}} / {{evidence.version}}</span></div></div>
  </section>
  }

  <section class="workspace-panel" style="margin-bottom:24px">
    <div class="panel-head"><div><h2>检查点排程</h2><p>每点给出目标干球温度、相对湿度、预计含水率和复查时间；间隔超过 8 小时提醒补测。</p></div><span class="count">{{checkpoints(item).length}} 个检查点</span></div>
    <div class="table-wrap"><table><thead><tr><th>#</th><th>复查时间</th><th>目标干球</th><th>目标相对湿度</th><th>预计含水率</th><th>间隔</th><th>补测提醒</th></tr></thead><tbody>
      <tr *ngFor="let point of checkpoints(item)"><td><strong>{{point.index}}</strong></td><td>{{formatDate(point.review_at)}}</td><td>{{point.target_dry_bulb_c}}°C</td><td>{{point.target_humidity_pct}}%</td><td>{{point.expected_moisture_pct}}%</td><td>{{point.interval_hours}}h</td><td>@if (point.supplemental_test) { <span class="state warm">需间隔中点补测成对读数</span> } @else { <span class="state good">按点复查</span> }<small>{{point.note}}</small></td></tr>
    </tbody></table></div>
  </section>

  @if (suggestions(item).length) {
  <section class="workspace-panel" style="margin-bottom:24px"><div class="panel-head"><div><h2>静态工艺建议</h2><p>检查点之外的单次调整证据，均限制在安全包络内。</p></div></div>
    <div class="rule-list" style="margin:0 26px 24px"><div *ngFor="let suggestion of suggestions(item)"><strong>{{suggestion.parameter}}<span *ngIf="suggestion.suggested"> · {{suggestion.current}} → {{suggestion.suggested}}</span></strong><span>{{suggestion.rule}}</span></div></div>
  </section>
  }

  <section class="workspace-panel"><div class="panel-head"><div><h2>说明</h2><p>{{item.explanation}}</p></div></div></section>
  } @else {
  <section class="workspace-panel"><div class="empty">尚无仿真计划，点击右上角“运行一次仿真”。</div></section>
  }

  <section class="workspace-panel" style="margin-top:24px"><div class="panel-head"><div><h2>计划版本</h2><p>冻结计划可作为后续比较的不可变基线；权限、审核与审计沿用现有流程。</p></div><span class="count">{{store.schedules().length}} 个版本</span></div><div class="table-wrap"><table><thead><tr><th>计划</th><th>批次</th><th>排程模式</th><th>风险</th><th>生命周期</th><th>冻结算偏差</th></tr></thead><tbody>
    <tr *ngFor="let row of store.schedules()" (click)="select(row.id)" style="cursor:pointer"><td><strong>{{row.id.slice(0,8)}}</strong><small>{{row.algorithm_version}} · v{{row.version}}</small></td><td>{{row.timber_lot_id.slice(0,8)}}</td><td><span class="state" [class.good]="row.plan_mode==='adaptive'" [class.warm]="row.plan_mode==='equalizing_retest'">{{modeName(row.plan_mode)}}</span></td><td><span class="risk" [class.low]="row.defect_risk_score<50" [class.high]="row.defect_risk_score>=50">{{row.defect_risk_score}} / 100</span></td><td><span class="state warm">{{row.schedule_state}}</span><small *ngIf="row.frozen_at">冻结于 {{formatDate(row.frozen_at)}}</small></td><td>{{frozenDelta(row)}}</td></tr>
  </tbody></table></div></section>
  `
})
export class SchedulesPageComponent {
  readonly store=inject(AppStore);
  readonly formatDate=formatDate;
  readonly formatSignedHours=formatSignedHours;
  error='';
  detail=signal<DryingSchedule|null>(null);

  select(id:string){void scheduleApi.get(id).then(item=>this.detail.set(item)).catch(err=>this.error=err instanceof Error?err.message:'读取计划失败')}

  async calculate(){
    const lot=this.store.lots()[0];
    if(!lot){this.error='请先登记批次';return}
    try{
      const created=await scheduleApi.calculate(lot.id);
      await this.store.refresh();
      this.detail.set(created);
    }catch(error){this.error=error instanceof Error?error.message:'计算失败'}
  }

  checkpoints(item:DryingSchedule):Checkpoint[]{return this.parse<Checkpoint[]>(item.checkpoints_json)||[]}
  stages(item:DryingSchedule):StageMetric[]{return this.parse<StageMetric[]>(item.stages_json)||[]}
  evidence(item:DryingSchedule):RuleEvidence[]{return this.parse<RuleEvidence[]>(item.rule_evidence_json)||[]}
  suggestions(item:DryingSchedule):Suggestion[]{return this.parse<Suggestion[]>(item.recommended_changes_json)||[]}
  failedEvidence(item:DryingSchedule):RuleEvidence[]{return this.evidence(item).filter(x=>!x.passed)}
  private parse<T>(raw:string):T|null{try{return raw?JSON.parse(raw) as T:null}catch{return null}}

  modeName(mode:string){return mode==='equalizing_retest'?'均衡复测':mode==='adaptive'?'自适应推进':(mode||'未排程')}
  stageName(stage:string){return {green:'生材期',fiber_saturation:'纤维饱和',bound_water:'吸着水',target:'目标平衡'}[stage]||stage}
  constraintName(name:string){return {temperature:'干球温度',relative_humidity:'相对湿度',moisture_gradient:'含水率梯度',drying_rate_pct_per_hour:'干燥速率',sample_pair_coverage:'成对采样覆盖'}[name]||name}
  evidenceLimit(item:DryingSchedule, constraint:string, pick:'min'|'max'):string{const rows=this.evidence(item).filter(x=>x.constraint===constraint).map(x=>x.limit);if(!rows.length)return '—';return String(pick==='min'?Math.min(...rows):Math.max(...rows))}
  frozenDelta(row:DryingSchedule):string{const frozen:FrozenPlanComparison|undefined|null=row.frozen_comparison;if(!frozen)return row.frozen_at?'本计划已冻结':'—';return `完成 ${formatSignedHours(frozen.finish_delta_hours)} · 风险 ${frozen.risk_delta>0?'+':''}${frozen.risk_delta}`}
}
