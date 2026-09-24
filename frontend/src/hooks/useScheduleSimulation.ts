import {inject} from '@angular/core';
import {AppStore} from '../stores/app.store';
import {scheduleApi, parseAdaptivePlan} from '../api/schedules';
import type {DryingSchedule, AdaptivePlan} from '../types/entities';

// useScheduleSimulation turns a schedule row into the adaptive 24h plan and
// keeps the same hash-based reuse: a repeat calculate call returns the stored
// plan instead of producing another version.
export function useScheduleSimulation() {
  const store = inject(AppStore);
  return {
    calculate: async (lotId:string):Promise<DryingSchedule> => {
      const schedule = await scheduleApi.calculate(lotId);
      await store.refresh();
      return schedule;
    },
    planFor: (schedule:DryingSchedule):AdaptivePlan|null => parseAdaptivePlan(schedule),
    review: (schedule:DryingSchedule, decision:string, note=''):Promise<DryingSchedule> =>
      scheduleApi.review(schedule.id, decision, schedule.version, note),
    freeze: (schedule:DryingSchedule):Promise<DryingSchedule> => scheduleApi.freeze(schedule.id, schedule.version),
  };
}
