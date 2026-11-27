import { useState } from 'react'
import { Keyboard, Platform, Pressable, StyleSheet, Text, View } from 'react-native'
import DateTimePicker, { DateTimePickerAndroid } from '@react-native-community/datetimepicker'
import { CalendarDays, ChevronDown, ChevronUp, Clock3 } from 'lucide-react-native'
import { ApiError, Controller, type Schedule, type ScheduleCreate, type ScheduleKind, type ScheduleTarget } from './api'
import { useLocale, type Language } from './locale'
import { weekdayLabels, intervalLabel } from '../../frontend/src/i18n/format'
import { automaticTaskTitle, initialScheduleForm, scheduleSubmission, ScheduleFormError, type ScheduleField, type ScheduleForm, type SimpleScheduleKind } from './scheduleForm'
import { Button, Field, Hint, SheetScroll, accent, edge, glass, ink, muted, s, wash } from './ui'

export const timingLabels: Record<ScheduleKind, string> = {
  at: '指定时间', after: '延时一次', every: '固定间隔', daily: '每天', weekly: '每周', cron: 'Cron', rrule: 'RRULE',
}
export const targetLabels: Record<ScheduleTarget['kind'], string> = {
  session: '已有会话', new_once: '首次新建，之后复用', new: '每次新建会话',
}
export function scheduleError(error: unknown) {
  if (error instanceof ApiError) {
    switch (error.code) {
      case 'schedule_conflict': return '任务刚有变化。请重新打开任务后再修改。'
      case 'schedule_execution_pending': return '任务仍在排队、执行或投递间隔内。'
      case 'schedule_not_found': return '此任务已删除。'
      case 'schedule_input_invalid': return '请检查任务内容和执行时间。'
    }
  }
  return '任务操作未完成，请重试。'
}
export function WeekdayLabels(language: Language) {
  return weekdayLabels(language)
}
const kinds: { kind: SimpleScheduleKind; label: string }[] = [
  { kind: 'at', label: '仅一次' }, { kind: 'daily', label: '每天' }, { kind: 'weekly', label: '每周' }, { kind: 'every', label: '固定间隔' },
]
const unitLabels = { minutes: '分钟', hours: '小时', days: '天' } as const
export function ScheduleEditor({ api, task, onSaved, onCancel }: {
  api: Controller; task?: Schedule; onSaved: () => void; onCancel: () => void
}) {
  const { t, language } = useLocale()
  const [initial] = useState(() => initialScheduleForm(task))
  const [form, setForm] = useState(initial)
  const [more, setMore] = useState(false)
  const [picker, setPicker] = useState<'date' | 'time'>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [fieldError, setFieldError] = useState<{ field: ScheduleField; message: string }>()
  const change = (patch: Partial<ScheduleForm>, timing = false) => {
    setForm(previous => ({ ...previous, ...patch, ...(timing && { timingChanged: true }) }))
    setFieldError(undefined); setError('')
  }
  const locale = language
  const timeLabel = form.when.toLocaleTimeString(locale, { timeZone: form.zone, hour: '2-digit', minute: '2-digit', hour12: false })
  const dateLabel = form.when.toLocaleDateString(locale, { timeZone: form.zone, year: 'numeric', month: 'short', day: 'numeric' })
  const days = WeekdayLabels(language)
  const summary = form.kind === 'at' ? `${dateLabel} ${timeLabel}` : form.kind === 'daily' ? `${t('每天')} ${timeLabel}`
    : form.kind === 'weekly' ? `${t('每周')} ${form.weekdays.slice().sort((a,b) => a-b).map(day => days[day - 1]).join(' · ')} ${timeLabel}`
    : form.kind === 'every' ? intervalLabel(language, Number(form.interval) || 0, { minutes: 'minute', hours: 'hour', days: 'day' }[form.unit] as 'minute' | 'hour' | 'day') : ''
  function chooseKind(kind: SimpleScheduleKind) {
    if (form.kind === kind) return
    setPicker(undefined)
    const patch: Partial<ScheduleForm> = { kind }
    if (!task && (kind === 'daily' || kind === 'weekly') && form.kind === 'at') {
      const nine = new Date(); nine.setHours(9, 0, 0, 0); patch.when = nine
    }
    change(patch, true)
  }
  function pick(mode: 'date' | 'time') {
    Keyboard.dismiss()
    if (Platform.OS === 'android') DateTimePickerAndroid.open({
      mode, value: form.when, timeZoneName: form.zone, is24Hour: true,
      positiveButton: { label: t('确定'), textColor: accent }, negativeButton: { label: t('取消'), textColor: muted },
      onValueChange: (_event, when) => change({ when }, true),
    })
    else setPicker(previous => previous === mode ? undefined : mode)
  }
  const inlineError = (field: ScheduleField) => fieldError?.field === field ? <Hint error>{t(fieldError.message)}</Hint> : null
  async function save() {
    Keyboard.dismiss(); setError(''); setFieldError(undefined)
    try {
      const input = scheduleSubmission(form, task, api.mainSessionId)
      setBusy(true)
      if (task) {
        if (Object.keys(input).length) await api.editSchedule(task.record.id, task.updatedAt ?? null, input)
      } else await api.createSchedule(input as ScheduleCreate)
      onSaved()
    } catch (cause) {
      if (cause instanceof ScheduleFormError) { setFieldError({ field: cause.field, message: cause.message }); if (cause.field === 'title') setMore(true) }
      else setError(cause instanceof Error && cause.message === '主会话未能准备完成。' ? cause.message : scheduleError(cause))
    } finally { setBusy(false) }
  }
  return <SheetScroll>
    <Field label={t('任务内容')} placeholder={t('需要助手到时做什么？')} value={form.prompt} editable={!busy} onChangeText={prompt => change({ prompt })} multiline />
    {inlineError('prompt')}
    <View style={styles.section}>
      <Text style={s.caption}>{t('执行时间')}</Text>
      {form.kind === 'custom' ? <>
        <Text style={s.body}>{t('保留现有时间安排')}</Text>
        <Hint>{t(task?.status === 'active' ? '下次运行' : '触发时间') + ' · ' + dateLabel + ' ' + timeLabel}</Hint>
        <Hint>{t('只修改任务内容或名称，不会改变原来的时间安排。')}</Hint>
        <Button label={t('修改执行时间')} disabled={busy} onPress={() => chooseKind('at')} />
      </> : <>
        <View style={styles.choices}>{kinds.map(({ kind, label }) => <Choice key={kind} label={t(label)} disabled={busy} selected={form.kind === kind} onPress={() => chooseKind(kind)} />)}</View>
        {(form.kind === 'at' || form.kind === 'daily' || form.kind === 'weekly') && <>
          <View style={styles.dateRow}>
            {form.kind === 'at' && <Pressable accessibilityRole="button" accessibilityLabel={t('日期') + ': ' + dateLabel} accessibilityState={{ expanded: picker === 'date' }} disabled={busy} onPress={() => pick('date')} style={({ pressed }) => [styles.dateButton, pressed && s.pressed]}>
              <CalendarDays size={18} color={muted} /><Text style={styles.dateText}>{dateLabel}</Text>
            </Pressable>}
            <Pressable accessibilityRole="button" accessibilityLabel={t('时间') + ': ' + timeLabel} accessibilityState={{ expanded: picker === 'time' }} disabled={busy} onPress={() => pick('time')} style={({ pressed }) => [styles.dateButton, pressed && s.pressed]}>
              <Clock3 size={18} color={muted} /><Text style={styles.dateText}>{timeLabel}</Text>
            </Pressable>
          </View>
          {picker && Platform.OS === 'ios' && <View style={styles.picker}>
            <DateTimePicker value={form.when} mode={picker} display="spinner" disabled={busy} timeZoneName={form.zone} locale={locale} themeVariant="light" textColor={ink} accentColor={accent} onValueChange={(_event, when) => change({ when }, true)} />
            <Button label={t('确定')} onPress={() => setPicker(undefined)} />
          </View>}
          <Hint>{t('时区') + ' · ' + form.zone}</Hint>
          {inlineError('time')}
        </>}
        {form.kind === 'weekly' && <>
          <View style={styles.choices}>{days.map((label, index) => <Choice key={index} label={label} disabled={busy} selected={form.weekdays.includes(index + 1)} onPress={() => change({ weekdays: form.weekdays.includes(index + 1) ? form.weekdays.filter(day => day !== index + 1) : [...form.weekdays, index + 1] }, true)} />)}</View>
          {inlineError('weekdays')}
        </>}
        {form.kind === 'every' && <>
          <Field label={t('每隔')} value={form.interval} editable={!busy} keyboardType="number-pad" onChangeText={interval => change({ interval }, true)} />
          <View style={styles.choices}>{(Object.keys(unitLabels) as (keyof typeof unitLabels)[]).map(unit => <Choice key={unit} label={t(unitLabels[unit])} disabled={busy} selected={form.unit === unit} onPress={() => change({ unit }, true)} />)}</View>
          {inlineError('interval')}
        </>}
        {task && form.timingChanged && <Button label={t('撤销时间修改')} disabled={busy} onPress={() => { setForm(previous => ({ ...initial, title: previous.title, prompt: previous.prompt })); setPicker(undefined); setFieldError(undefined) }} />}
        <View style={styles.summary}><Clock3 size={16} color={muted} /><Text style={[s.meta, { flex: 1 }]}>{summary}</Text></View>
      </>}
    </View>
    <Pressable accessibilityRole="button" accessibilityLabel={t('更多选项')} accessibilityState={{ expanded: more }} onPress={() => setMore(value => !value)} style={({ pressed }) => [styles.more, pressed && s.pressed]}>
      <Text style={s.caption}>{t('更多选项')}</Text>{more ? <ChevronUp size={17} color={muted} /> : <ChevronDown size={17} color={muted} />}
    </Pressable>
    {more && <><Field label={t('任务名称（选填）')} placeholder={automaticTaskTitle(form.prompt) || t('根据任务内容自动命名')} value={form.title} editable={!busy} maxLength={120} onChangeText={title => change({ title })} />{inlineError('title')}</>}
    <Hint error>{error && t(error)}</Hint>
    <Button label={t(task ? '保存修改' : '创建任务')} primary busy={busy} disabled={!form.prompt.trim()} onPress={() => void save()} />
    <Button label={t('取消')} disabled={busy} onPress={onCancel} />
  </SheetScroll>
}
export function Choice({ label, selected, onPress, disabled = false }: { label: string; selected: boolean; onPress: () => void; disabled?: boolean }) {
  return <Pressable accessibilityRole="button" accessibilityState={{ selected, disabled }} disabled={disabled} onPress={onPress}
    style={({ pressed }) => [styles.choice, selected && styles.selected, pressed && s.pressed]}>
    <Text numberOfLines={1} style={[s.caption, selected && { color: accent }]}>{label}</Text>
  </Pressable>
}
const styles = StyleSheet.create({
  section: { gap: 12 }, choices: { flexDirection: 'row', flexWrap: 'wrap', gap: 7 },
  choice: { minHeight: 38, borderWidth: 1, borderColor: edge, borderRadius: 13, backgroundColor: glass, justifyContent: 'center', paddingHorizontal: 11 },
  selected: { backgroundColor: wash, borderColor: 'transparent' },
  dateRow: { flexDirection: 'row', flexWrap: 'wrap', gap: 10 },
  dateButton: { flexDirection: 'row', alignItems: 'center', gap: 9, minHeight: 48, paddingHorizontal: 12, borderRadius: 14, borderWidth: StyleSheet.hairlineWidth, borderColor: edge, backgroundColor: glass },
  dateText: { color: ink, fontSize: 15 }, picker: { backgroundColor: glass, borderRadius: 16, overflow: 'hidden', paddingBottom: 8 },
  more: { minHeight: 44, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  summary: { flexDirection: 'row', gap: 8, alignItems: 'center', paddingVertical: 10 },
})
