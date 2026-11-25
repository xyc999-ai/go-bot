import { useEffect, useRef, useState } from 'react'
import { Alert, AppState, Switch, Text, View } from 'react-native'
import { Clock3, RefreshCw, Sparkles } from 'lucide-react-native'
import { proactiveEnableProblem, proactivePhase, proactiveProblem, type ProactiveStatus } from '../../frontend/src/settings/proactiveState'
import { ApiError, Controller } from './api'
import { useLocale } from './locale'
import { accent, Button, Hint, muted, Panel, s, SheetScroll } from './ui'

export function Proactive({ api, onModels }: { api: Controller; onModels: () => void }) {
  const { t, language } = useLocale()
  const [status, setStatus] = useState<ProactiveStatus>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [retry, setRetry] = useState(false)
  const generation = useRef(0)
  const saving = useRef(false)
  const date = (at: number) => new Date(at).toLocaleString(language === 'zh-CN' ? 'zh-CN' : 'en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })

  async function refresh(signal?: AbortSignal) {
    if (saving.current) return
    const version = generation.current
    try {
      const next = await api.proactiveSettings(signal)
      if (!signal?.aborted && version === generation.current) { setStatus(next); setError(current => current === 'Could not load proactive settings.' ? '' : current) }
    } catch { if (!signal?.aborted && version === generation.current) setError('Could not load proactive settings.') }
  }
  useEffect(() => {
    const controller = new AbortController()
    void refresh(controller.signal)
    const timer = setInterval(() => { if (AppState.currentState === 'active') void refresh(controller.signal) }, 15000)
    const foreground = AppState.addEventListener('change', state => { if (state === 'active') void refresh(controller.signal) })
    return () => { controller.abort(); clearInterval(timer); foreground.remove(); generation.current++ }
  }, [api])

  async function save(enabled: boolean) {
    generation.current++
    saving.current = true
    setBusy(true); setError(''); setRetry(false)
    try { setStatus(await api.saveProactiveSettings(enabled)) }
    catch (issue) {
      const modelProblem = issue instanceof ApiError && enabled ? proactiveEnableProblem(issue.code) : ''
      if (modelProblem) {
        Alert.alert(t('配置模型'), t(modelProblem), [
          { text: t('取消'), style: 'cancel' },
          { text: t('Open model settings'), onPress: onModels },
        ])
      } else if (issue instanceof ApiError && issue.code === 'settings_saved_host_apply_failed') {
        setStatus(await api.proactiveSettings().catch(() => undefined))
        setError('Settings were saved, but the Host could not apply them. Retry or reopen the account.')
        setRetry(true)
      } else setError('Could not save proactive settings.')
    } finally { saving.current = false; setBusy(false) }
  }

  const issue = status && proactiveProblem(status)
  return <SheetScroll>
    <Hint>{t('Keeps up with your recent conversations and memory, investigates useful opportunities, and shares relevant findings in your main chat.')}</Hint>
    <Hint error>{t(error)}</Hint>
    {!status ? !error && <Hint>{t('Loading proactive settings…')}</Hint> : <>
      <Panel style={{ paddingVertical: 22, gap: 14 }}>
        <View style={[s.row, { gap: 12 }]}>
          <Sparkles size={21} color={accent} strokeWidth={1.6} />
          <Text style={[s.body, { flex: 1, fontWeight: '600' }]}>{t('Enable proactive assistance')}</Text>
          <Switch accessibilityLabel={t('Enable proactive assistance')} value={status.enabled} disabled={busy} onValueChange={enabled => void save(enabled)} trackColor={{ false: '#d5d9e4', true: accent }} thumbColor="#fff" />
        </View>
      </Panel>
      {__DEV__ && <Panel style={{ paddingVertical: 22, gap: 22 }}>
        <View style={[s.row, { gap: 9 }]}><View style={{ width: 7, height: 7, borderRadius: 4, backgroundColor: !status.enabled ? '#a1a8b5' : status.error ? '#ba9260' : '#799a8b' }} /><Text style={[s.caption, { flex: 1 }]}>{t(proactivePhase(status))}</Text></View>
        <View style={{ gap: 10 }}>
          <View style={[s.row, { gap: 8 }]}><Clock3 size={17} color={muted} strokeWidth={1.7} /><Text style={s.caption}>{t('Next check')}</Text></View>
          <Text style={[s.body, { fontSize: 19, fontWeight: '600', lineHeight: 27 }]}>{status.enabled ? status.nextCheckAt ? date(status.nextCheckAt) : t('Waiting for the first check') : t('No checks while turned off')}</Text>
          {status.enabled && !!status.nextCheckReason && <Text style={[s.caption, { lineHeight: 21 }]}>{status.nextCheckReason}</Text>}
        </View>
        {!!status.lastCompletedAt && <View style={{ gap: 7 }}><Text style={s.caption}>{t('Last completed')}</Text><Text style={s.body}>{date(status.lastCompletedAt)}</Text></View>}
      </Panel>}
      {!!issue && <Hint>{t(issue)}</Hint>}
      {!!status.error?.startsWith('model_') && status.enabled && <Button label={t('Open model settings')} onPress={onModels} />}
    </>}
    <Hint>{t('Uses the main chat’s model and connected services while your Host is running. Model and service usage may incur charges.')}</Hint>
    {(__DEV__ || retry) && <Button label={t(retry ? 'Retry applying' : 'Refresh status')} disabled={busy} onPress={() => { setError(''); void (retry && status ? save(status.enabled) : refresh()) }}><RefreshCw size={16} color={accent} /></Button>}
  </SheetScroll>
}
