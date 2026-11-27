import { useEffect, useRef, useState } from 'react'
import { Alert, Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from 'react-native'
import { CalendarDays, ChartNoAxesColumnIncreasing, ChevronRight, Clock3, FileText, FolderOpen, MessageSquare, Plus, Send, type LucideIcon } from 'lucide-react-native'
import { Controller, problem, type Schedule, type ScheduleState } from './api'
import { useLocale, type Language } from './locale'
import { intervalLabel } from '../../frontend/src/i18n/format'
import type { TranscriptCache } from '../../frontend/src/chat/transcriptCache'
import { ScheduleEditor, WeekdayLabels, scheduleError, targetLabels, timingLabels } from './ScheduleEditor'
import { accent, Button, Hint, IconButton, ink, muted, Panel, s, success, Sheet, SheetScroll, TintIcon, type PageChrome } from './ui'

const labels = { active: '启用中', paused: '已暂停', ended: '已结束' }
const colors = { active: '#f0f1f2', paused: '#f0f1f2', ended: '#edf3ef' }
const detailSurfaceColors = ['#f4f4f5', '#f4f4f5', '#f4f4f5'] as const
const time = (language: Language, value?: string) => {
  const date = value ? new Date(value) : null
  return date && Number.isFinite(date.getTime()) && date.getFullYear() > 1
    ? date.toLocaleString(language, { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }) : '—'
}

function ruleSummary(task: Schedule, language: Language, t: (label: string) => string) {
  const { record } = task
  if (record.kind === 'at' || record.kind === 'after' || (record.kind === 'rrule' && /[;:]COUNT=1(?:;|$)/.test(record.rrule ?? ''))) return t('仅一次')
  if (record.kind === 'every') {
    const seconds = record.everySeconds ?? 0
    const unit = seconds % 3600 === 0 ? ['小时', 3600] as const : seconds % 60 === 0 ? ['分钟', 60] as const : ['秒', 1] as const
    return intervalLabel(language, seconds / unit[1], unit[0] === '小时' ? 'hour' : unit[0] === '分钟' ? 'minute' : 'second')
  }
  if (record.kind === 'daily' || record.kind === 'weekly') {
    const days = record.weekdays?.map(day => WeekdayLabels(language)[day - 1]).join(' · ')
    return [t(timingLabels[record.kind]), days, record.time?.slice(0, 5)].filter(Boolean).join(' · ')
  }
  const frequency = record.rrule?.match(/FREQ=(MINUTELY|HOURLY|DAILY|WEEKLY|MONTHLY|YEARLY)/)?.[1]
  const frequencies: Record<string, string> = { MINUTELY: '按分钟重复', HOURLY: '按小时重复', DAILY: '按日重复', WEEKLY: '按周重复', MONTHLY: '按月重复', YEARLY: '按年重复' }
  return frequency ? t(frequencies[frequency]) : t('自定义规则')
}

const taskTabs = [{ id: 'upcoming', label: '待执行' }, { id: 'completed', label: '已完成' }] as const
type TaskTab = typeof taskTabs[number]['id']
type Screen = { mode: 'create' } | { mode: 'detail'; id: string } | { mode: 'edit'; task: Schedule }

function groupTasks(tasks: Schedule[], tab: TaskTab, clock: number, language: Language) {
  const dateKey = (date: Date) => [date.getFullYear(), date.getMonth(), date.getDate()].join('/')
  const today = new Date(clock)
  const groups = new Map<string, { title: string; items: Schedule[] }>()
  for (const task of tasks) {
    const date = new Date(tab === 'completed' ? task.lastDelivery?.deliveredAt ?? task.record.scheduledAt : task.record.scheduledAt)
    const key = task.status !== 'paused' && date.getFullYear() > 1 ? dateKey(date) : 'undated'
    if (!groups.has(key)) {
      const title = key === 'undated' ? '' : date.toLocaleDateString(language, { month: 'short', day: 'numeric', year: date.getFullYear() === today.getFullYear() ? undefined : 'numeric' })
      groups.set(key, { title, items: [] })
    }
    groups.get(key)!.items.push(task)
  }
  return Array.from(groups, ([key, group]) => ({ key, ...group }))
}

function taskIcon(kind: Schedule['record']['kind']) {
  switch (kind) {
    case 'every': return { Icon: ChartNoAxesColumnIncreasing, background: '#f1f2f3', color: '#666a70' }
    case 'daily': case 'weekly': case 'cron': return { Icon: CalendarDays, background: '#f1f2f3', color: '#666a70' }
    default: return { Icon: FileText, background: '#f1f2f3', color: '#666a70' }
  }
}

export function Tasks({ api, cache, active, openChat, header, navigation, bottomInset = 0 }: PageChrome & { api: Controller; cache: TranscriptCache; active: boolean; openChat: (id: string) => void }) {
  const { t, language } = useLocale()

  const [tasks, setTasks] = useState<Schedule[]>([])
  const [tab, setTab] = useState<TaskTab>('upcoming')
  const [screen, setScreen] = useState<Screen>()
  const [refreshing, setRefreshing] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState('')
  const [actionError, setActionError] = useState('')
  const [feedback, setFeedback] = useState('')
  const [busy, setBusy] = useState(false)
  const requestID = useRef(0)
  const [clock, setClock] = useState(Date.now())

  async function refresh(fromPull = false) {
    const request = ++requestID.current
    if (fromPull) setRefreshing(true)
    try {
      const { tasks } = await api.schedules()
      if (request === requestID.current) { setTasks(tasks); setLoaded(true); setError('') }
    } catch (cause) {
      if (request === requestID.current) setError(problem(cause, '无法读取任务，请下拉重试。'))
    } finally {
      if (request === requestID.current) setRefreshing(false)
    }
  }

  useEffect(() => {
    if (!active) return
    let coalesce: ReturnType<typeof setTimeout> | undefined
    const changed = () => {
      clearTimeout(coalesce)
      coalesce = setTimeout(() => void refresh(), 100)
    }
    const unsubscribe = cache.subscribe('@schedule', changed)
    void refresh()
    const timer = setInterval(() => setClock(Date.now()), 1000)
    return () => {
      ++requestID.current
      unsubscribe()
      clearInterval(timer)
      clearTimeout(coalesce)
    }
  }, [api, active, cache])

  const selected = screen?.mode === 'detail' ? screen.id : ''
  const task = tasks.find(item => item.record.id === selected)
  const shown = tasks.filter(item => (item.status === 'ended') === (tab === 'completed'))
    .sort((a, b) => tab === 'completed'
      ? (b.lastDelivery?.deliveredAt ?? b.record.scheduledAt).localeCompare(a.lastDelivery?.deliveredAt ?? a.record.scheduledAt)
      : Number(a.status === 'paused') - Number(b.status === 'paused') || a.record.scheduledAt.localeCompare(b.record.scheduledAt))
  const groups = groupTasks(shown, tab, clock, language)

  function show(value?: Screen) {
    setActionError('')
    setFeedback('')
    setScreen(value)
  }

  async function mutate(action: () => Promise<unknown>, message = '', close = false) {
    setBusy(true)
    setActionError('')
    setFeedback('')
    try {
      await action()
      if (close) setScreen(undefined)
      await refresh()
      setFeedback(message)
    } catch (cause) {
      setActionError(scheduleError(cause))
      await refresh()
    } finally {
      setBusy(false)
    }
  }

  return <>
    <ScrollView stickyHeaderIndices={navigation ? [1] : undefined} contentContainerStyle={{ paddingBottom: bottomInset + 24 }} refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void refresh(true)} />}>
      <View>{header}</View>
      {navigation && <View style={{ backgroundColor: '#fff' }}>{navigation}</View>}
      <View style={styles.page}>
      <View>
      <View style={styles.heading}>
        <Text accessibilityRole="header" style={styles.sectionTitle}>{t('Scheduled tasks')}</Text>
        <IconButton label={t('新建任务')} style={styles.add} disabled={busy} onPress={() => show({ mode: 'create' })}>
          <Plus size={22} color={ink} strokeWidth={1.75} />
        </IconButton>
      </View>
      <View accessibilityRole="tablist" style={styles.tabs}>{taskTabs.map(({ id, label }) =>
        <Pressable key={id} accessibilityRole="tab" accessibilityState={{ selected: id === tab }}
          onPress={() => setTab(id)} style={[styles.tab, id === tab && styles.selectedTab]}>
          <Text style={[styles.tabLabel, id === tab && styles.selectedLabel]}>{t(label)}</Text>
        </Pressable>)}</View>
      </View>
      <Hint error>{t(error || (!screen ? actionError : ''))}</Hint>
      {!screen && <Hint>{t(feedback)}</Hint>}
      {!shown.length && loaded && !error && <Panel style={styles.empty}>
        <CalendarDays color={accent} size={27} />
        <Text style={s.body}>{t(tab === 'completed' ? '暂无已完成任务' : '暂无待执行任务')}</Text>
        {!tasks.length && tab === 'upcoming' && <Hint>{t('在会话中让助手安排任务后，任务会出现在这里。')}</Hint>}
      </Panel>}
      {groups.map(group => <View key={group.key} style={styles.group}>
        {!!group.title && <Text accessibilityRole="header" style={styles.groupTitle}>{group.title}</Text>}
        {group.items.map(item => <TaskRow key={item.record.id} task={item} onPress={() => show({ mode: 'detail', id: item.record.id })} />)}
      </View>)}
      {tab === 'completed' && shown.length > 0 &&
        <Button label={t('清理已结束任务')} dangerTone disabled={busy} onPress={() => Alert.alert(t('清理已结束任务？'), t('将删除任务和投递历史。会话记录会保留。'), [
          { text: t('取消'), style: 'cancel' },
          { text: t('删除'), style: 'destructive', onPress: () => void mutate(() => api.deleteEndedSchedules()) },
        ])} />}
      </View>
    </ScrollView>
    {active && screen && <Sheet title={t(screen.mode === 'create' ? '新建任务' : screen.mode === 'edit' ? '编辑任务' : '任务详情')}
      hideTitle={screen.mode === 'detail' && !!task} surfaceColors={screen.mode === 'detail' ? detailSurfaceColors : undefined}
      onClose={() => show()}>{afterClose => {
        const open = (id: string) => afterClose(() => { show(); openChat(id) })
        if (screen.mode === 'create' || screen.mode === 'edit') {
          const original = screen.mode === 'edit' ? screen.task : undefined
          const returnTo = () => show(original ? { mode: 'detail', id: original.record.id } : undefined)
          return <ScheduleEditor key={original?.record.id ?? 'create'} api={api} task={original}
            onSaved={() => { returnTo(); void refresh() }} onCancel={returnTo} />
        }
        if (!task) return <SheetScroll><Hint>{t('此任务已删除。')}</Hint></SheetScroll>
        const id = task.record.id
        const linked = task.target?.kind === 'session' ? task.target.sessionId : task.lastDelivery?.targetSessionId
        const held = !!task.executionActive
        const limited = !!task.deliveryFloorEndsAt && Date.parse(task.deliveryFloorEndsAt) > clock
        const target = task.target ? t(targetLabels[task.target.kind]) : t('已有会话')
        const needsSession = !linked && !!task.target && task.target.kind !== 'session'
        const { Icon, background, color } = taskIcon(task.record.kind)
        return <SheetScroll>
          <View style={styles.detailHeading}>
            <View style={[styles.detailGlyph, { backgroundColor: background }]}><Icon size={28} color={color} strokeWidth={1.75} /></View>
            <View style={styles.detailTitleWrap}>
              <Text accessibilityRole="header" style={styles.detailTitle}>{task.record.title}</Text>
              <TaskBadge status={task.status} />
            </View>
          </View>
          <DetailCard icon={Clock3} label={t(task.status === 'active' ? '下次运行' : '触发规则')}
            value={task.status === 'active' ? time(language, task.record.scheduledAt) : ruleSummary(task, language, t)}
            note={[task.status === 'active' && ruleSummary(task, language, t), task.record.timeZone].filter(Boolean).join(' · ')} />
          <DetailCard icon={MessageSquare} label={t('投递目标')}
            value={target + (needsSession ? ' · ' + t('首次投递时创建') : '')} note={task.record.prompt} />
          {!!task.target?.create?.cwd && <DetailCard icon={FolderOpen} label={t('工作目录')} value={task.target.create.cwd} />}
          <DetailCard icon={Send} label={t('上次投递')} value={task.lastDelivery ? time(language, task.lastDelivery.deliveredAt) : '—'} />
          {held && <Hint>{t('上一条投递仍在排队或执行。')}</Hint>}
          {limited && <Hint>{t('下次可立即运行') + ' · ' + time(language, task.deliveryFloorEndsAt)}</Hint>}
          <Hint error>{t(actionError)}</Hint>
          <Hint>{t(feedback)}</Hint>
          <Button label={t('立即运行')} primary disabled={held || limited || busy} onPress={() => void mutate(() => api.runSchedule(id), '指令已投递到会话。')} />
          {linked && <Button label={t('打开会话')} onPress={() => open(linked)} />}
          <View style={styles.wrap}>
            {task.status !== 'ended' && <Button label={t(task.status === 'active' ? '暂停任务' : '恢复任务')} disabled={held || busy}
              onPress={() => void mutate(() => api.editSchedule(id, task.updatedAt ?? null, { active: task.status !== 'active' }))} />}
            <Button label={t('编辑任务')} disabled={held || busy} onPress={() => show({ mode: 'edit', task })} />
            <Button label={t('删除任务')} dangerTone disabled={held || busy} onPress={() => Alert.alert(t('删除任务？'), t('将删除任务和投递历史。会话记录会保留。'), [
              { text: t('取消'), style: 'cancel' },
              { text: t('删除'), style: 'destructive', onPress: () => void mutate(() => api.deleteSchedule(id, task.updatedAt ?? null), '', true) },
            ])} />
          </View>
        </SheetScroll>
      }}</Sheet>}
    </>
}

function DetailCard({ icon: Icon, label, value, note }: { icon: LucideIcon; label: string; value: string; note?: string }) {
  return <Panel style={styles.detailCard}>
    <Icon size={19} color={muted} strokeWidth={1.65} style={styles.detailCardIcon} />
    <View style={styles.detailCardCopy}>
      <Text style={styles.rowCaption}>{label}</Text>
      <Text style={styles.detailValue}>{value}</Text>
      {!!note && <Text style={s.meta}>{note}</Text>}
    </View>
  </Panel>
}

function TaskRow({ task, onPress }: { task: Schedule; onPress: () => void }) {
  const { language, t } = useLocale()
  const { Icon, background, color } = taskIcon(task.record.kind)
  const hour = (value: string) => {
    const date = new Date(value)
    return date.getFullYear() > 1 ? date.toLocaleTimeString(language, { hour: 'numeric', minute: '2-digit' }) : '—'
  }
  let caption = ruleSummary(task, language, t)
  if (task.status === 'active') caption = task.executionActive ? t('排队或执行中') : t('下次运行') + ' · ' + hour(task.record.scheduledAt)
  if (task.status === 'ended' && task.lastDelivery) caption = t('上次投递') + ' · ' + hour(task.lastDelivery.deliveredAt)
  return <Pressable accessibilityRole="button" onPress={onPress} style={({ pressed }) => [styles.row, pressed && s.pressed]}>
    <TintIcon background={background}><Icon size={21} color={color} strokeWidth={1.75} /></TintIcon>
    <View style={styles.rowCopy}>
      <Text style={styles.taskTitle} numberOfLines={2}>{task.record.title}</Text>
      <Text style={styles.rowCaption} numberOfLines={2}>{caption}</Text>
    </View>
    <TaskBadge status={task.status} list />
    <ChevronRight color={muted} size={16} />
  </Pressable>
}

function TaskBadge({ status, list }: { status: ScheduleState; list?: boolean }) {
  const { t } = useLocale()
  return <Text style={[styles.badge, list && styles.listBadge, { backgroundColor: colors[status], color: status === 'ended' ? success : muted }]}>{t(list && status === 'ended' ? '已完成' : labels[status])}</Text>
}

const styles = StyleSheet.create({
  page: { paddingHorizontal: 20, paddingTop: 14, paddingBottom: 8, gap: 14, maxWidth: 620, width: '100%', alignSelf: 'center' },
  heading: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  sectionTitle: { fontSize: 18, fontWeight: '500', color: ink, letterSpacing: -.3 },
  add: { borderRadius: 22 },
  tabs: { flexDirection: 'row', gap: 26, borderBottomWidth: StyleSheet.hairlineWidth, borderColor: '#eceef0' },
  tab: { minHeight: 42, alignItems: 'center', justifyContent: 'center', paddingHorizontal: 2, borderBottomWidth: 2, borderColor: 'transparent' },
  selectedTab: { borderBottomColor: ink },
  tabLabel: { color: muted, fontSize: 15, fontWeight: '500', textAlign: 'center' },
  selectedLabel: { color: ink, fontWeight: '600' },
  group: { gap: 10 },
  groupTitle: { fontSize: 13, fontWeight: '500', letterSpacing: 0, color: ink, marginHorizontal: 3, marginBottom: 1 },
  row: { flexDirection: 'row', alignItems: 'center', gap: 10, paddingHorizontal: 12, paddingVertical: 13, minHeight: 70, borderRadius: 15,
    backgroundColor: '#f6f6f7', borderWidth: StyleSheet.hairlineWidth, borderColor: '#f0f1f2' },
  rowCopy: { flex: 1, gap: 3 },
  rowCaption: { color: muted, fontSize: 14, lineHeight: 20 },
  wrap: { flexDirection: 'row', flexWrap: 'wrap', gap: 7 },
  empty: { alignItems: 'center', paddingVertical: 27 },
  taskTitle: { fontSize: 16, fontWeight: '500', color: ink },
  badge: { alignSelf: 'flex-start', color: accent, fontSize: 11, fontWeight: '600', paddingHorizontal: 8, paddingVertical: 5, borderRadius: 10, overflow: 'hidden' },
  listBadge: { alignSelf: 'center', paddingVertical: 4 },
  detailHeading: { flexDirection: 'row', alignItems: 'center', gap: 13, marginBottom: 10, paddingHorizontal: 4 },
  detailGlyph: { width: 52, height: 52, borderRadius: 16, alignItems: 'center', justifyContent: 'center', borderWidth: 1, borderColor: 'rgba(255,255,255,0.82)' },
  detailTitleWrap: { flex: 1, gap: 6 },
  detailTitle: { fontSize: 21, fontWeight: '600', letterSpacing: -0.5, color: ink },
  detailCard: {
    flexDirection: 'row', paddingVertical: 14, paddingHorizontal: 16, gap: 14, borderRadius: 16,
    backgroundColor: '#fff', borderColor: '#e2e3e6', borderWidth: StyleSheet.hairlineWidth,
  },
  detailCardIcon: { marginTop: 2 },
  detailCardCopy: { flex: 1, gap: 4 },
  detailValue: { color: ink, fontSize: 15, lineHeight: 21 },
})
