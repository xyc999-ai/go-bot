import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowLeft, ArrowUpRight, CalendarClock, CalendarDays, CheckCheck, Clock3, FolderOpen, MessageSquare, RefreshCw, Repeat2, Send, type LucideIcon } from 'lucide-react'
import { request } from '../api/controller'
import { useLocale } from '../locale'
import type { TranscriptCache } from '../chat/transcriptCache'
import { scheduleGroup, scheduleRule, scheduleSession, scheduleTime, type Schedule } from './schedules'

export function Tasks({ userId, cache, onChat }: { userId: string; cache: TranscriptCache; onChat: (id: string) => void }) {
  const { t, language } = useLocale()
  const [tasks, setTasks] = useState<Schedule[]>([])
  const [tab, setTab] = useState<'upcoming' | 'completed'>('upcoming')
  const [selected, select] = useState<string>()
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const inFlight = useRef<AbortController | undefined>(undefined)
  const load = useCallback(async () => {
    inFlight.current?.abort()
    const controller = new AbortController()
    inFlight.current = controller
    setBusy(true)
    try {
      const page = await request<{ tasks: Schedule[] }>('/v1/host/schedules', { signal: controller.signal }, userId)
      if (!controller.signal.aborted) { setTasks(page.tasks ?? []); setError('') }
    } catch { if (!controller.signal.aborted) setError('Could not load tasks. Try again.') }
    finally { if (!controller.signal.aborted) setBusy(false) }
  }, [userId])
  useEffect(() => {
    void load()
    const off = cache.subscribe('@schedule', () => { void load() })
    return () => { off(); inFlight.current?.abort() }
  }, [cache, load])
  const shown = tasks.filter(task => scheduleGroup(task) === tab).sort((a, b) => a.record.scheduledAt.localeCompare(b.record.scheduledAt))
  const task = shown.find(task => task.record.id === selected) ?? shown[0]
  const linked = task && scheduleSession(task)
  const status = { active: 'Active', paused: 'Paused', ended: 'Completed' }
  return <section className={'tasks-page page-enter' + (selected ? ' task-detail-open' : '')}>
    <div className="page-intro"><div><h1>{t('Tasks')}</h1><p>{t('Scheduled work, all in one place.')}</p></div><button className="icon-button" title={t('Refresh tasks')} aria-label={t('Refresh tasks')} disabled={busy} onClick={() => void load()}><RefreshCw size={18} className={busy ? 'spinner' : ''} /></button></div>
    <div className="task-tabs" role="tablist" aria-label={t('Tasks')}>{(['upcoming', 'completed'] as const).map(value => <button key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => { setTab(value); select(undefined) }}>
      {value === 'upcoming' ? <CalendarClock size={16} /> : <CheckCheck size={16} />}{t(value === 'upcoming' ? 'Upcoming' : 'Completed')}<span>{tasks.filter(task => scheduleGroup(task) === value).length}</span>
    </button>)}</div>
    {error && <p className="feedback" role="alert">{t(error)}</p>}
    {!tasks.length && busy ? <div className="page-empty"><CalendarClock size={30} /><p>{t('Loading tasks…')}</p></div> : !shown.length ? <div className="page-empty"><CalendarDays size={32} strokeWidth={1.3} /><h2>{t(tab === 'upcoming' ? 'No upcoming tasks' : 'No completed tasks')}</h2><p>{t('Ask your agent to schedule something in a conversation.')}</p></div> : <div className="tasks-body">
      <div className="task-list" role="group" aria-label={t(tab === 'upcoming' ? 'Upcoming' : 'Completed')}>{shown.map(item => <button type="button" key={item.record.id} className={'task-row' + (item === task ? ' selected' : '')} onClick={() => select(item.record.id)}>
        <span className="task-row-icon"><CalendarClock size={18} strokeWidth={1.65} /></span><span className="task-row-copy"><strong>{item.record.title}</strong><span>{scheduleRule(item, language, t)}</span><small>{item.status === 'active' ? scheduleTime(item.record.scheduledAt, language, item.record.timeZone) : t(status[item.status])}</small></span><ArrowUpRight size={15} />
      </button>)}</div>
      {task && <article className="task-detail page-enter" key={task.record.id}>
        <button type="button" className="task-back text-button" onClick={() => select(undefined)}><ArrowLeft size={16} />{t('Tasks')}</button>
        <header className="task-detail-header"><span className="task-detail-icon"><CalendarClock size={25} strokeWidth={1.5} /></span><span className={'status-badge status-' + task.status}>{t(status[task.status])}</span><h2>{task.record.title}</h2></header>
        <dl className="task-facts">
          <TaskFact Icon={Clock3} label={t('Next run')} value={task.status === 'active' ? scheduleTime(task.record.scheduledAt, language, task.record.timeZone) : '—'} note={task.record.timeZone} />
          <TaskFact Icon={Repeat2} label={t('Schedule')} value={scheduleRule(task, language, t)} note={task.record.expression || task.record.rrule} />
          <TaskFact Icon={MessageSquare} label={t('Delivery target')} value={t(task.target?.kind === 'new' ? 'New conversation each time' : task.target?.kind === 'new_once' ? 'Create a conversation once' : 'Existing conversation')} />
          <TaskFact Icon={Send} label={t('Last delivery')} value={scheduleTime(task.lastDelivery?.deliveredAt, language, task.record.timeZone)} />
          {task.target?.create?.cwd && <TaskFact Icon={FolderOpen} label={t('Working directory')} value={task.target.create.cwd} />}
        </dl>
        <div className="task-prompt"><h3>{t('Instructions')}</h3><p>{task.record.prompt}</p></div>
        {task.executionActive && <p className="subtle-notice">{t('The last delivery is queued or running.')}</p>}
        {linked && <button type="button" className="primary-button task-open-chat" onClick={() => onChat(linked)}>{t('Open conversation')}<ArrowUpRight size={16} /></button>}
      </article>}
    </div>}
  </section>
}
function TaskFact({ Icon, label, value, note }: { Icon: LucideIcon; label: string; value: string; note?: string }) {
  return <div><Icon size={17} strokeWidth={1.6} /><dt>{label}</dt><dd>{value}{note && <small>{note}</small>}</dd></div>
}
