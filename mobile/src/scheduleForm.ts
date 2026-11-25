import type { Schedule, ScheduleCreate, SchedulePatch, ScheduleTiming } from './api'

export type SimpleScheduleKind = 'at' | 'daily' | 'weekly' | 'every'
export type IntervalUnit = 'minutes' | 'hours' | 'days'
export type ScheduleForm = {
  title: string; prompt: string; kind: SimpleScheduleKind | 'custom'; when: Date; zone: string
  weekdays: number[]; interval: string; unit: IntervalUnit; timingChanged: boolean
}
export type ScheduleField = 'prompt' | 'title' | 'time' | 'weekdays' | 'interval'
export class ScheduleFormError extends Error {
  readonly field: ScheduleField
  constructor(field: ScheduleField, message: string) { super(message); this.field = field }
}
export const intervalSeconds = { minutes: 60, hours: 3600, days: 86400 } as const
export function clockParts(date: Date, zone: string) {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(date)
  const get = (type: string) => parts.find(part => part.type === type)!.value
  return { date: `${get('year')}-${get('month')}-${get('day')}`, time: `${get('hour')}:${get('minute')}:00` }
}
export function initialScheduleForm(task?: Schedule, now = new Date()): ScheduleForm {
  const record = task?.record
  const nextHour = new Date(now)
  nextHour.setHours(nextHour.getHours() + 1, 0, 0, 0)
  const seconds = record?.everySeconds ?? 3600
  const unit: IntervalUnit = seconds % 86400 === 0 ? 'days' : seconds % 3600 === 0 ? 'hours' : 'minutes'
  const kind = record?.kind === 'after' ? 'at' : record?.kind ?? 'at'
  const editable = kind !== 'cron' && kind !== 'rrule' && !(kind === 'every' && seconds % 60 !== 0)
  return {
    title: record?.title ?? '', prompt: record?.prompt ?? '', kind: editable ? kind as SimpleScheduleKind : 'custom',
    when: record ? new Date(record.scheduledAt) : nextHour,
    zone: record?.timeZone || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
    weekdays: record?.weekdays?.slice() ?? [((now.getDay() + 6) % 7) + 1],
    interval: editable ? String(seconds / intervalSeconds[unit]) : '1', unit: editable ? unit : 'hours', timingChanged: !task,
  }
}
export function automaticTaskTitle(prompt: string) {
  return Array.from(prompt.trim().split(/\r?\n/).find(line => line.trim())?.trim() ?? '').slice(0, 40).join('')
}
export function formTiming(form: ScheduleForm, now = new Date()): ScheduleTiming {
  const { kind, when, zone, weekdays } = form
  if (kind === 'custom') throw new ScheduleFormError('time', '请选择新的执行时间。')
  if (kind === 'every') {
    const amount = Number(form.interval)
    const seconds = amount * intervalSeconds[form.unit]
    if (!Number.isSafeInteger(amount) || amount < 1 || !Number.isSafeInteger(seconds) || seconds > 253402300799)
      throw new ScheduleFormError('interval', '请输入大于零的整数间隔。')
    return { kind, every_seconds: seconds }
  }
  if (!Number.isFinite(when.getTime())) throw new ScheduleFormError('time', '请选择有效的日期和时间。')
  if (kind === 'at') {
    const minute = Math.floor(when.getTime() / 60000) * 60000
    if (minute <= now.getTime()) throw new ScheduleFormError('time', '请选择未来的执行时间。')
    // The native picker already resolved a real instant, including DST folds.
    return { kind, at: new Date(minute).toISOString() }
  }
  const local = clockParts(when, zone)
  const clock = { time: local.time, time_zone: zone }
  if (kind === 'daily') return { kind, daily: clock }
  if (!weekdays.length) throw new ScheduleFormError('weekdays', '请至少选择一天。')
  return { kind, weekly: { ...clock, weekdays: [...new Set(weekdays)].sort((a, b) => a - b) } }
}
export function scheduleSubmission(form: ScheduleForm, task: Schedule | undefined, mainSessionId: string, now = new Date()): ScheduleCreate | SchedulePatch {
  const prompt = form.prompt.trim(), title = form.title.trim() || automaticTaskTitle(prompt)
  if (!prompt) throw new ScheduleFormError('prompt', '请填写任务内容。')
  if (Array.from(title).length > 120) throw new ScheduleFormError('title', '任务名称不能超过 120 个字符。')
  if (task) {
    const patch: SchedulePatch = {}
    if (title !== task.record.title) patch.title = title
    if (prompt !== task.record.prompt) patch.prompt = prompt
    if (form.timingChanged) patch.timing = formTiming(form, now)
    return patch
  }
  if (!mainSessionId) throw new Error('主会话未能准备完成。')
  return { title, prompt, timing: formTiming(form, now), target: { kind: 'session', sessionId: mainSessionId } }
}
