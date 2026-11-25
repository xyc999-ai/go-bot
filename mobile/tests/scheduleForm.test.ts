import test from 'node:test'
import assert from 'node:assert/strict'
import { automaticTaskTitle, clockParts, initialScheduleForm, scheduleSubmission, ScheduleFormError } from '../src/scheduleForm.ts'
import type { Schedule, ScheduleCreate } from '../src/api.ts'
const now = new Date('2026-11-26T02:30:10Z')
const custom: Schedule = {
  record: { id: 'custom', title: 'Monthly report', prompt: 'Review the sources', kind: 'rrule', scheduledAt: '2026-12-01T01:00:00.000Z', rrule: 'DTSTART;TZID=Asia/Shanghai:20261201T090000\nRRULE:FREQ=MONTHLY' },
  status: 'active', updatedAt: '2026-11-26T01:00:00.000Z', target: { kind: 'new', create: { cwd: '/workspace/research', agentPreset: 'custom' } },
}
test('creating with only content and a time supplies a title and the main destination', () => {
  const form = initialScheduleForm(undefined, now)
  form.prompt = '  整理 AI 行业动态\n请附上来源  '
  const input = scheduleSubmission(form, undefined, 'main', now) as ScheduleCreate
  assert.equal(input.title, '整理 AI 行业动态')
  assert.equal(input.prompt, '整理 AI 行业动态\n请附上来源')
  assert.deepEqual(input.target, { kind: 'session', sessionId: 'main' })
  assert.equal(form.when.getMinutes(), 0)
  assert.equal(form.when.getSeconds(), 0)
  assert.ok(form.when > now)
})
test('title derivation handles Unicode without splitting surrogate pairs', () => {
  assert.equal(Array.from(automaticTaskTitle('🐳'.repeat(60))).length, 40)
})
test('editing content or name retains advanced timing, destination, workspace and preset', () => {
  const original = structuredClone(custom)
  const form = initialScheduleForm(custom, now)
  assert.equal(form.kind, 'custom')
  form.prompt = 'Review updated sources'
  assert.deepEqual(scheduleSubmission(form, custom, 'main', now), { prompt: 'Review updated sources' })
  form.title = 'New title'
  assert.deepEqual(scheduleSubmission(form, custom, 'main', now), { title: 'New title', prompt: 'Review updated sources' })
  assert.deepEqual(custom, original)
})
test('weekly edits keep the stored timezone even when the device is elsewhere', () => {
  const task: Schedule = { ...custom, record: { ...custom.record, kind: 'weekly', time: '09:00:30', timeZone: 'Asia/Shanghai', weekdays: [1,3], rrule: undefined } }
  const form = initialScheduleForm(task, now)
  assert.deepEqual(clockParts(form.when, form.zone), { date: '2026-12-01', time: '09:00:00' })
  assert.deepEqual(scheduleSubmission(form, task, 'main', now), {})
  form.weekdays = [5,1]; form.timingChanged = true
  assert.deepEqual(scheduleSubmission(form, task, 'main', now), { timing: { kind: 'weekly', weekly: { time: '09:00:00', time_zone: 'Asia/Shanghai', weekdays: [1,5] } } })
})
test('past occurrences are preserved on content edits, but rejected when explicitly rescheduled', () => {
  const task: Schedule = { ...custom, record: { ...custom.record, kind: 'after', scheduledAt: '2026-11-25T01:00:00Z' }, status: 'ended' }
  const form = initialScheduleForm(task, now); form.prompt = 'Revised content'
  assert.deepEqual(scheduleSubmission(form, task, 'main', now), { prompt: 'Revised content' })
  form.timingChanged = true
  assert.throws(() => scheduleSubmission(form, task, 'main', now), (error: unknown) => error instanceof ScheduleFormError && error.field === 'time')
})
test('interval units convert to seconds and invalid or empty-weekday rules fail locally', () => {
  const form = initialScheduleForm(undefined, now)
  form.prompt = 'Check'; form.kind = 'every'; form.interval = '2'; form.unit = 'hours'
  assert.deepEqual((scheduleSubmission(form, undefined, 'main', now) as ScheduleCreate).timing, { kind: 'every', every_seconds: 7200 })
  for (const interval of ['0', '-1', '1.5', 'NaN', '']) {
    form.interval = interval
    assert.throws(() => scheduleSubmission(form, undefined, 'main', now), ScheduleFormError)
  }
  form.kind = 'weekly'; form.weekdays = []
  assert.throws(() => scheduleSubmission(form, undefined, 'main', now), (error: unknown) => error instanceof ScheduleFormError && error.field === 'weekdays')
})
test('odd-second intervals are retained rather than silently rounded', () => {
  const task: Schedule = { ...custom, record: { ...custom.record, kind: 'every', everySeconds: 97, rrule: undefined } }
  const form = initialScheduleForm(task, now)
  assert.equal(form.kind, 'custom')
  assert.deepEqual(scheduleSubmission(form, task, 'main', now), {})
})

test('wall-clock formatting follows saved IANA zone across daylight saving and date boundaries', () => {
  assert.deepEqual(clockParts(new Date('2027-03-14T06:30:00Z'), 'America/New_York'), { date: '2027-03-14', time: '01:30:00' })
  assert.deepEqual(clockParts(new Date('2027-03-14T07:30:00Z'), 'America/New_York'), { date: '2027-03-14', time: '03:30:00' })
  assert.deepEqual(clockParts(new Date('2026-11-26T01:00:00Z'), 'America/Los_Angeles'), { date: '2026-11-25', time: '17:00:00' })
})
test('one-time selection retains the exact instant during a repeated DST hour', () => {
  const before = new Date('2027-11-06T00:00:00Z')
  const form = initialScheduleForm(undefined, before)
  form.prompt = 'Reminder'; form.zone = 'America/New_York'
  for (const instant of ['2027-11-07T05:30:00.000Z', '2027-11-07T06:30:00.000Z']) {
    form.when = new Date(instant)
    assert.deepEqual((scheduleSubmission(form, undefined, 'main', before) as ScheduleCreate).timing, { kind: 'at', at: instant })
  }
})
