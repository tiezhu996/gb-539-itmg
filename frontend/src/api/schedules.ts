import {request} from './client';
import type {DryingSchedule, AdaptivePlan} from '../types/entities';

export const scheduleApi = {
  list:():Promise<DryingSchedule[]> => request<DryingSchedule[]>('/schedules'),
  get:(id:string):Promise<DryingSchedule> => request<DryingSchedule>(`/schedules/${id}`),
  calculate:(lotId:string):Promise<DryingSchedule> => request<DryingSchedule>('/schedules/calculate',{method:'POST',body:JSON.stringify({timber_lot_id:lotId})}),
  review:(id:string,decision:string,version:number,note=''):Promise<DryingSchedule> => request<DryingSchedule>(`/schedules/${id}/review`,{method:'POST',body:JSON.stringify({decision,note,version})}),
  freeze:(id:string,version:number):Promise<DryingSchedule> => request<DryingSchedule>(`/schedules/${id}/freeze`,{method:'POST',body:JSON.stringify({version})}),
  compare:(id:string,baselineScheduleId:string):Promise<unknown> => request(`/schedules/${id}/compare`,{method:'POST',body:JSON.stringify({baseline_schedule_id:baselineScheduleId})}),
};

export function parseAdaptivePlan(schedule:DryingSchedule):AdaptivePlan|null {
  if (!schedule.adaptive_plan_json) return null;
  try { return JSON.parse(schedule.adaptive_plan_json) as AdaptivePlan; }
  catch { return null; }
}
