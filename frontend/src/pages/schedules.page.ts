import {Component, computed, inject, signal} from '@angular/core';
import {NgFor, NgIf} from '@angular/common';
import {FormsModule} from '@angular/forms';
import {AppStore} from '../stores/app.store';
import {useScheduleSimulation} from '../hooks/useScheduleSimulation';
import {parseAdaptivePlan} from '../api/schedules';
import {formatDate} from '../utils/format';
import type {DryingSchedule, AdaptivePlan} from '../types/entities';

@Component({
  standalone: true,
  imports: [NgFor, NgIf, FormsModule],
  template: `
  <div class="page-head">
    <div>
      <p class="eyebrow">04 / SCHEDULES</p>
      <h1>曲线优化 · 自适应排程</h1>
      <p class="description">每次仿真按当前阶段和最近两次成对读数排出未来 24 小时三个检查点；规则未通过时只排均衡复测，不升温、不降湿。</p>
    </div>
    <button class="button primary" (click)="calculate()">运行一次仿真</button>
  </div>

  <div class="schedule-banner">
    <div class="banner-mark">∿</div>
    <div>
      <strong>算法版本 {{algorithmVersion}} · 24 小时三检查点自适应排程</strong>
      <p>温度取材料规则与窑炉边界的更小值、相对湿度取更大值；相同输入仍按输入哈希复用原计划，权限、审核与审计流程不变。建议仅离线参考，不替代现场安全联锁。</p>
    </div>
  </div>
  <p class="form-error">{{error}}</p>

  <section class="workspace-panel schedule-picker">
    <div class="panel-head">
      <div><h2>计划版本</h2><p>选择一个版本查看 24 小时检查点；冻结计划是不可变基线。</p></div>
      <span class="count">{{store.schedules().length}} 个版本</span>
    </div>
    <div class="table-wrap">
      <table>
        <thead><tr><th>计划</th><th>批次</th><th>风险</th><th>完成时间</th><th>生命周期</th><th></th></tr></thead>
        <tbody>
          <tr *ngFor="let item of store.schedules()" [class.row-selected]="selectedId()===item.id">
            <td><strong>{{item.id.slice(0,8)}}</strong><small>{{item.algorithm_version}} · v{{item.version}}</small></td>
            <td>{{item.timber_lot_id.slice(0,8)}}</td>
            <td><span class="risk" [class.low]="item.defect_risk_score<50" [class.high]="item.defect_risk_score>=50">{{item.defect_risk_score}} / 100</span></td>
            <td>{{item.predicted_finish_at ? formatDate(item.predicted_finish_at) : '—'}}</td>
            <td>
              <span class="state" [class.good]="item.schedule_state==='accepted'" [class.warm]="item.schedule_state!=='accepted'">
                {{item.frozen_at ? '已冻结' : stateLabel(item.schedule_state)}}
              </span>
            </td>
            <td><button class="text-button" (click)="select(item.id)">{{selectedId()===item.id?'正在查看':'查看排程'}}</button></td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>

  <ng-container *ngIf="selected() as schedule">
    <ng-container *ngIf="planOf(schedule) as plan; else noPlan">
      <div class="insight-strip">
        <div><span class="strip-label">排程模式</span><strong>{{plan.mode==='adaptive'?'自适应':'均衡复测'}}</strong><small>{{stageLabel(plan.stage)}}</small></div>
        <div><span class="strip-label">复查节奏</span><strong>{{plan.cadence_hours}}<small> 小时</small></strong><small>窗口至 {{formatDate(plan.window_until)}}</small></div>
        <div><span class="strip-label">当前含水率 / 目标</span><strong>{{plan.current_moisture_pct}}<small>%</small></strong><small>目标 {{plan.target_moisture_pct}}%</small></div>
        <div class="strip-note">{{plan.basis}}</div>
      </div>

      <div *ngIf="plan.mode==='equalizing_hold'" class="schedule-banner hold-banner">
        <div class="banner-mark">!</div>
        <div>
          <strong>先均衡复测：{{plan.hold_reasons?.join('、')}}</strong>
          <p>以下检查点只复测、不升温、不降湿；中心/表层成对读数回到规则内后再重新仿真。</p>
        </div>
      </div>

      <div *ngIf="plan.warnings.length" class="recheck-alerts">
        <div *ngFor="let warning of plan.warnings" class="recheck-alert">⏱ {{warning}}</div>
      </div>

      <section class="workspace-panel">
        <div class="panel-head"><div><h2>未来 24 小时三个检查点</h2><p>每点给出目标干球温度、相对湿度、预计含水率与复查时间；超过 8 小时间隔提醒补测成对读数。</p></div></div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>#</th><th>动作</th><th>复查时间</th><th>间隔</th><th>目标干球</th><th>相对湿度</th><th>预计含水率</th><th>补测</th></tr></thead>
            <tbody>
              <tr *ngFor="let cp of plan.checkpoints">
                <td><strong>{{cp.sequence}}</strong></td>
                <td><span class="state" [class.good]="cp.action==='adaptive_follow'" [class.warm]="cp.action==='equalizing_recheck'">{{cp.action==='adaptive_follow'?'自适应跟进':'均衡复测'}}</span></td>
                <td>{{formatDate(cp.review_at)}}<small>{{cp.note}}</small></td>
                <td>{{cp.interval_hours}} h</td>
                <td>{{cp.target_dry_bulb_c}} °C</td>
                <td>{{cp.target_humidity_pct}}%</td>
                <td>{{cp.predicted_moisture_pct}}%</td>
                <td><span *ngIf="cp.interim_reminder" class="state warm">{{cp.interim_reminder_at?formatDate(cp.interim_reminder_at):''}} 补测</span><span *ngIf="!cp.interim_reminder" class="state good">按点复查</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="workspace-panel envelope-panel">
        <div class="panel-head"><div><h2>有效安全包线（取更严格一侧）</h2><p>温度上限取材料规则与窑炉边界的更小值；湿度下限取更大值；梯度与干燥速率受材料规则约束。</p></div></div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>参数</th><th>材料规则</th><th>窑炉边界</th><th>生效限制</th><th>约束方</th></tr></thead>
            <tbody>
              <tr *ngFor="let limit of plan.effective_envelope">
                <td><strong>{{limitLabel(limit.parameter)}}</strong></td>
                <td>{{limit.ConstrainedBy?limit.material_limit:'—'}}{{limitSuffix(limit.parameter)}}</td>
                <td>{{limit.kiln_limit?limit.kiln_limit+' '+limitSuffix(limit.parameter):'无硬限制'}}</td>
                <td>{{limit.effective_limit}}{{limitSuffix(limit.parameter)}}</td>
                <td><span class="entity">{{limit.constrained_by==='kiln'?'窑炉更严':'材料更严'}}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section *ngIf="schedule.frozen_baseline_view as view" class="workspace-panel baseline-panel">
        <div class="panel-head"><div><h2>与所冻结算的对比</h2><p>本计划完成时间与风险差对照冻结基线 {{view.baseline_schedule_id.slice(0,8)}}。</p></div></div>
        <div class="metrics baseline-metrics">
          <div><span>本计划完成时间</span><strong>{{view.current_finish_at?formatDate(view.current_finish_at):'—'}}</strong><small>冻结基线 {{view.baseline_finish_at?formatDate(view.baseline_finish_at):'—'}}</small></div>
          <div><span>完成时间差</span><strong [class.ahead]="view.finish_delta_hours<0" [class.behind]="view.finish_delta_hours>0">{{signedHours(view.finish_delta_hours)}}</strong><small>负值表示早于冻结基线</small></div>
          <div><span>风险差</span><strong [class.ahead]="view.risk_delta<0" [class.behind]="view.risk_delta>0">{{signed(view.risk_delta)}}</strong><small>本计划 {{view.current_risk_score}} / 基线 {{view.baseline_risk_score}}</small></div>
        </div>
      </section>

      <section class="workspace-panel review-panel">
        <div class="panel-head"><div><h2>人工审核与冻结</h2><p>创建者不能接受自己的建议；接受后由审核角色冻结，全部操作沿用现有审计。</p></div></div>
        <div class="review-actions">
          <button class="button" [disabled]="!canReview(schedule)" (click)="review(schedule,'reviewed')">标记已复核</button>
          <button class="button primary" [disabled]="!canAccept(schedule)" (click)="review(schedule,'accepted')">接受建议</button>
          <button class="button" [disabled]="!canFreeze(schedule)" (click)="freeze(schedule)">冻结为基线</button>
        </div>
        <p class="review-note">当前版本 v{{schedule.version}} · {{stateLabel(schedule.schedule_state)}}{{schedule.reviewed_by?' · 审核人 '+schedule.reviewed_by:''}}</p>
      </section>
    </ng-container>
    <ng-template #noPlan>
      <section class="workspace-panel"><p class="empty">{{schedule.schedule_state==='failed'?('计算未通过校验：'+schedule.failure_reason):'该版本没有自适应排程，请重新运行仿真。'}}</p></section>
    </ng-template>
  </ng-container>
  <section *ngIf="!selected() && store.schedules().length===0" class="workspace-panel"><p class="empty">还没有计划：登记批次、导入成对读数后运行一次仿真。</p></section>
  `,
})
export class SchedulesPageComponent {
  readonly store = inject(AppStore);
  private readonly simulation = useScheduleSimulation();
  readonly algorithmVersion = 'curve-v2.1';
  private readonly pickedId = signal<string>('');
  readonly selected = computed<DryingSchedule|undefined>(() => {
    const list = this.store.schedules();
    return list.find(item => item.id === this.pickedId()) ?? list[0];
  });
  readonly selectedId = computed<string>(() => this.selected()?.id ?? '');
  error = '';
  protected readonly formatDate = formatDate;

  select(id:string):void { this.pickedId.set(id); }

  planOf(schedule:DryingSchedule):AdaptivePlan|null { return parseAdaptivePlan(schedule); }

  async calculate():Promise<void> {
    const lot = this.store.lots()[0];
    if (!lot) { this.error = '请先登记批次'; return; }
    try {
      this.error = '';
      await this.simulation.calculate(lot.id);
    } catch (error) { this.error = error instanceof Error ? error.message : '计算失败'; }
  }

  async review(schedule:DryingSchedule, decision:string):Promise<void> {
    try { this.error=''; await this.simulation.review(schedule, decision); await this.store.refresh(); }
    catch (error) { this.error = error instanceof Error ? error.message : '审核失败'; }
  }
  async freeze(schedule:DryingSchedule):Promise<void> {
    try { this.error=''; await this.simulation.freeze(schedule); await this.store.refresh(); }
    catch (error) { this.error = error instanceof Error ? error.message : '冻结失败'; }
  }

  canReview(schedule:DryingSchedule):boolean { return schedule.schedule_state==='proposed'; }
  canAccept(schedule:DryingSchedule):boolean { return schedule.schedule_state==='proposed' || schedule.schedule_state==='reviewed'; }
  canFreeze(schedule:DryingSchedule):boolean { return schedule.schedule_state==='accepted' && !schedule.frozen_at; }

  stateLabel(state:string):string {
    return ({draft:'草稿', calculating:'计算中', proposed:'待审核', failed:'计算失败', reviewed:'已复核', accepted:'已接受', voided:'已作废'} as Record<string,string>)[state] ?? state;
  }
  stageLabel(stage:string):string {
    return ({green:'生材阶段', fiber_saturation:'纤维饱和', bound_water:'吸着水', target:'目标均衡'} as Record<string,string>)[stage] ?? stage;
  }
  limitLabel(parameter:string):string {
    return ({temperature:'干球温度上限', relative_humidity:'相对湿度下限', moisture_gradient:'含水率梯度', drying_rate_pct_per_hour:'干燥速率'} as Record<string,string>)[parameter] ?? parameter;
  }
  limitSuffix(parameter:string):string {
    if (parameter==='temperature') return ' °C';
    if (parameter==='relative_humidity') return '%';
    if (parameter==='drying_rate_pct_per_hour') return ' %/h';
    return '';
  }
  signedHours(value:number):string { return `${value>0?'+':''}${value.toFixed(1)} h`; }
  signed(value:number):string { return `${value>0?'+':''}${value.toFixed(1)}`; }
}
