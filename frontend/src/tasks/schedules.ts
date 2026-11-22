export type Schedule = {
  record: { id: string; title: string; prompt: string; kind: 'after' | 'at' | 'every' | 'daily' | 'weekly' | 'cron' | 'rrule'; scheduledAt: string; time?: string; timeZone?: string; weekdays?: number[]; expression?: string; everySeconds?: number; rrule?: string }
  status: 'active' | 'paused' | 'ended'
  target?: { kind: 'session' | 'new' | 'new_once'; sessionId?: string; create?: { cwd?: string; agentPreset?: string } }
  sessionId?: string
  lastDelivery?: { scheduledAt: string; deliveredAt: string; targetSessionId?: string; messageId: string }
  executionActive?: boolean
  deliveryFloorEndsAt?: string
}
export function scheduleGroup(task: Schedule) { return task.status === 'ended' ? 'completed' : 'upcoming' }
export function scheduleSession(task: Schedule) {
  return task.target?.kind === 'session' ? task.target.sessionId : task.lastDelivery?.targetSessionId ?? task.sessionId
}
export function scheduleTime(value: string | undefined, language: string, timeZone?: string) {
  const date = value ? new Date(value) : null
  return date && Number.isFinite(date.getTime()) && date.getFullYear() > 1
    ? date.toLocaleString(language, { timeZone, month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit' }) : '—'
}
export function scheduleRule(task: Schedule, language: string, t: (value: string) => string) {
  const rule = task.record
  if (rule.kind === 'after' || rule.kind === 'at') return t('One time')
  if (rule.kind === 'every') {
    const seconds = rule.everySeconds ?? 0
    const unit = seconds % 3600 === 0 ? ['hours', 3600] as const : seconds % 60 === 0 ? ['minutes', 60] as const : ['seconds', 1] as const
    return t('Every') + ' ' + seconds / unit[1] + ' ' + t(unit[0])
  }
  if (rule.kind === 'daily' || rule.kind === 'weekly') {
    const dayNames = language === 'zh-CN' ? ['一', '二', '三', '四', '五', '六', '日'] : ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']
    return [t(rule.kind === 'daily' ? 'Daily' : 'Weekly'), rule.weekdays?.map(day => dayNames[day - 1]).join(' · '), rule.time?.slice(0, 5)].filter(Boolean).join(' · ')
  }
  return t('Custom rule')
}
