import { useEffect, useMemo, useState } from 'react'
import { ActivityIndicator, AppState, Linking, Pressable, StyleSheet, Text, View } from 'react-native'
import { Check, ExternalLink, KeyRound } from 'lucide-react-native'
import type { AuthorizationEntry, AuthorizationPrompt, MCPServer } from '../../../frontend/src/api/extensions'
import { authorizationLabel, authorizationURL, useAuthorization } from '../../../frontend/src/extensions/useAuthorization'
import { useLocale } from '../locale'
import { accent, Button, edge, Field, Hint, ink, muted, Panel, s, success } from '../ui'
import { authorizationAPI, mcpAuthorizationAPI } from './api'
import { useLoopbackAuthorization } from '../../../frontend/src/extensions/useLoopbackAuthorization'
import { loopbackReceiver } from './loopbackReceiver'
import { useMCPAuthorization } from '../../../frontend/src/extensions/useMCPAuthorization'
import { authorizationEntryLabels, connectionLabel, connectionTitle, mcpAuthorizationLabel, mcpAuthorizationRunning } from '../../../frontend/src/extensions/connections'
import { Choice } from './Choice'
import { Feedback, TextAction, Toolbar, type ScreenProps, x } from './shared'

export function usePluginAuthorizations({ api, cache }: Pick<ScreenProps, 'api' | 'cache'>, instanceIds: string[]) {
  const transport = useMemo(() => authorizationAPI(api), [api]), mcpAPI = useMemo(() => mcpAuthorizationAPI(api), [api])
  const state = useAuthorization(transport, cache), mcp = useMCPAuthorization(mcpAPI, cache, instanceIds)
  const entries = state.entries.filter(entry => instanceIds.some(id => entry.key.startsWith(id + '/')))
  const scope = instanceIds.join('\0')
  useEffect(() => { state.close(); mcp.close() }, [scope])
  const busy = state.busy || mcp.busy, reload = () => { state.reload(); mcp.reload() }
  useEffect(() => { const listener = AppState.addEventListener('change', value => { if (value === 'active') { state.reload(); mcp.reload() } }); return () => listener.remove() }, [state.reload, mcp.reload])
  return { state, mcp, entries, busy, reload }
}
export function Authorizations({ connections, onBusyChange, onBack }: Pick<ScreenProps, 'onBusyChange'> & { connections: ReturnType<typeof usePluginAuthorizations>; onBack: () => void }) {
  const { state, mcp, entries, busy, reload } = connections, { t } = useLocale(), attempt = state.attempt
  const local = useLoopbackAuthorization(state, loopbackReceiver)
  useEffect(() => { onBusyChange(busy); return () => onBusyChange(false) }, [busy, onBusyChange])
  return <>
    <Toolbar title={attempt ? attempt.label : mcp.selected ? connectionTitle(mcp.selected) : 'Accounts and authorization'} busy={busy} onBack={() => { if (attempt) state.close(); else if (mcp.attempt) mcp.close(); else onBack() }} onRefresh={reload} />
    {attempt ? <>
      <View style={a.status}><KeyRound size={19} color={muted} strokeWidth={1.6} /><Text style={s.meta}>{t(authorizationLabel[attempt.status])}</Text></View>
      {attempt.notices.filter(notice => notice.url !== local.authorizationUrl || !notice.url).map((notice, index) => <Panel key={index} style={a.panel}>
        <Text selectable style={a.message}>{t(notice.message)}</Text>
        {attempt.status === 'running' && notice.code && <Text selectable style={a.code}>{notice.code}</Text>}
        {attempt.status === 'running' && authorizationURL(notice.url) && <Pressable accessibilityRole="link" onPress={() => { void Linking.openURL(authorizationURL(notice.url)!).catch(() => {}) }} style={a.link}><Text style={a.linkText}>{t('Continue in browser')}</Text><ExternalLink size={15} color={accent} /></Pressable>}
      </Panel>)}
      {local.prompt && <Panel style={a.panel}><Text style={a.message}>{t('Complete authorization in your browser. Go Bot will receive the result automatically.')}</Text><Button primary label={t(local.phase === 'submitting' ? 'Submitting authorization…' : local.phase === 'waiting' ? 'Waiting for browser authorization…' : 'Continue in browser')} busy={local.phase === 'waiting' || local.phase === 'submitting'} disabled={state.busy || local.phase !== 'idle'} onPress={() => { void local.begin() }} /></Panel>}
      <Hint error>{local.error ? t(local.error) : undefined}</Hint>
      {attempt.prompts.filter(prompt => prompt.id !== local.prompt?.id).map(prompt => <Question key={prompt.id} prompt={prompt} busy={state.busy} onAnswer={(answer, decline) => { void state.answer(prompt.id, answer, decline) }} />)}
      {attempt.status === 'running' && !attempt.prompts.length && <View style={a.status}><ActivityIndicator color={accent} size="small" /><Text style={[s.meta, { flex: 1 }]}>{t('Waiting for authorization to finish…')}</Text></View>}
      {attempt.status === 'authorized' && <View style={a.status}><Check size={20} color={success} /><Text style={[s.body, { color: success, flex: 1 }]}>{t('Authorization is ready.')}</Text></View>}
      {(attempt.status === 'failed' || attempt.status === 'cancelled') && <Button primary label={t('Restart authorization')} disabled={state.busy} onPress={() => { void state.restart() }} />}
      <Hint error>{attempt.error ? t(attempt.error) : undefined}</Hint><Button label={t(attempt.status === 'running' ? 'Cancel authorization' : 'Done')} disabled={state.busy} onPress={() => { if (attempt.status === 'running') void state.cancel(); else state.close() }} />
      {attempt.status === 'running' && <Hint>{t('Closing this panel keeps the authorization running. You can return to it from this plugin.')}</Hint>}
    </> : mcp.attempt && mcp.selected ? <MCPDetails state={mcp} /> : <>
      <Hint>{t('Connect accounts using the authorization methods provided by your plugins.')}</Hint>
      {entries.map(entry => <Account key={entry.key} entry={entry} onDisconnect={() => { void state.disconnect(entry.key) }} busy={busy} onCancel={() => { void state.cancelKey(entry.key) }} onStart={method => { if (entry.inFlight && entry.attemptId) void state.open(entry.attemptId); else void state.start(entry.key, method) }} onView={() => { if (entry.attemptId) void state.open(entry.attemptId) }} />)}
      {mcp.servers.map(server => <MCPAccount key={server.name} server={server} busy={busy} onStart={() => { void mcp.open(server) }} onView={() => mcp.resume(server)} />)}
      {!entries.length && !mcp.servers.length && <Hint>{t('No account authorization methods are available.')}</Hint>}
    </>}
    <Feedback error={attempt ? state.error : mcp.attempt ? mcp.error : state.error || mcp.error} notice="" />
  </>
}
function Account({ entry, busy, onStart, onCancel, onView, onDisconnect }: { entry: AuthorizationEntry; busy: boolean; onStart: (method: string) => void; onCancel: () => void; onView: () => void; onDisconnect: () => void }) {
  const { t } = useLocale(), [method, setMethod] = useState(entry.methods[0]?.id ?? '')
  const labels = authorizationEntryLabels(entry)
  return <Panel style={a.panel}><View style={a.status}><KeyRound size={19} color={muted} strokeWidth={1.6} /><View style={x.identity}><Text style={x.heading}>{entry.label}</Text><Text style={s.meta}>{t(labels.status)}{entry.accountLabel ? ' · ' + entry.accountLabel : ''}</Text></View></View>
    {entry.methods.length > 1 && <Choice label="Authorization method" value={method} options={entry.methods.map(method => ({ value: method.id, label: t(method.label) }))} disabled={busy || entry.inFlight} onChange={setMethod} />}
    <TextAction label={labels.start} disabled={busy} onPress={() => onStart(method)} />
    {entry.configured && entry.canDisconnect && !entry.inFlight && <TextAction label={labels.disconnect} disabled={busy} onPress={onDisconnect} />}
    {entry.inFlight && <TextAction label="Cancel authorization" disabled={busy} onPress={onCancel} />}
    {!entry.inFlight && entry.attemptId && <TextAction label="View last result" disabled={busy} onPress={onView} />}
  </Panel>
}
function MCPAccount({ server, busy, onStart, onView }: { server: MCPServer; busy: boolean; onStart: () => void; onView: () => void }) {
 const { t } = useLocale(), active = mcpAuthorizationRunning(server.authorization)
 return <Panel style={a.panel}><View style={a.status}><KeyRound size={19} color={muted} strokeWidth={1.6} /><View style={x.identity}><Text style={x.heading}>{connectionTitle(server)}</Text><Text style={s.meta}>{t(connectionLabel(server))}</Text></View></View><TextAction label={active ? 'Continue authorization' : 'Connect in browser'} disabled={busy} onPress={onStart} />{!active && server.authorization?.id && <TextAction label="View last result" disabled={busy} onPress={onView} />}</Panel>
}
function MCPDetails({ state }: { state: ReturnType<typeof useMCPAuthorization> }) {
 const { t } = useLocale(), attempt = state.attempt!, server = state.selected!, active = mcpAuthorizationRunning(attempt), url = authorizationURL(attempt.url)
 return <><View style={a.status}><KeyRound size={19} color={muted} /><Text style={s.meta}>{t(mcpAuthorizationLabel(attempt))}</Text></View>
  {active && url && <Panel style={a.panel}><Text style={a.message}>{t('Authorize access on the service website, then return here.')}</Text><Pressable accessibilityRole="link" style={a.link} onPress={() => { void Linking.openURL(url).catch(() => {}) }}><Text style={a.linkText}>{t('Continue in browser')}</Text><ExternalLink size={15} color={accent} /></Pressable></Panel>}
  {active && <View style={a.status}><ActivityIndicator size="small" color={accent} /><Text style={[s.meta, { flex: 1 }]}>{t('Waiting for authorization to finish…')}</Text></View>}
  {attempt.phase === 'authorized' && <View style={a.status}><Check size={20} color={success} /><Text style={[s.body, { flex: 1 }]}>{t('Authorization is ready.')}</Text></View>}
  <Hint error>{attempt.error || server.error}</Hint><Button label={t(active ? 'Cancel authorization' : 'Done')} disabled={state.busy} onPress={() => { if (active) void state.cancel(); else state.close() }} />
  {active && <Hint>{t('Closing this panel keeps the authorization running. You can return to it from this plugin.')}</Hint>}
 </>
}
function Question({ prompt, busy, onAnswer }: { prompt: AuthorizationPrompt; busy: boolean; onAnswer: (answer?: string, decline?: boolean) => void }) {
  const { t } = useLocale(), [answer, setAnswer] = useState('')
  return <Panel style={a.panel}>
    {prompt.kind === 'text' && prompt.loopback && authorizationURL(prompt.loopback.authorizationUrl) && <Pressable accessibilityRole="link" style={a.link} onPress={() => { void Linking.openURL(authorizationURL(prompt.loopback!.authorizationUrl)!).catch(() => {}) }}><Text style={a.linkText}>{t('Continue in browser')}</Text><ExternalLink size={15} color={accent} /></Pressable>}
    {prompt.kind === 'select' ? <>
      <Text style={a.question}>{t(prompt.message)}</Text>
      {prompt.options.map(option => <Pressable key={option.id} accessibilityRole="radio" accessibilityState={{ checked: answer === option.id, disabled: busy }} disabled={busy} onPress={() => setAnswer(option.id)} style={({ pressed }) => [a.option, answer === option.id && a.selected, pressed && s.pressed]}>
        <View style={[a.radio, answer === option.id && { borderColor: accent }]}>{answer === option.id && <View style={a.dot} />}</View><View style={{ flex: 1, gap: 5 }}><Text style={s.body}>{t(option.label)}</Text>{option.description && <Text style={s.meta}>{t(option.description)}</Text>}</View>
      </Pressable>)}
    </> : <Field label={t(prompt.message)} value={answer} onChangeText={setAnswer} placeholder={prompt.placeholder} editable={!busy} secureTextEntry={prompt.kind === 'secret'} autoCapitalize="none" autoCorrect={false} maxLength={16384} />}
    <Button primary label={t('Continue')} busy={busy} disabled={prompt.kind === 'select' && !answer} onPress={() => { onAnswer(answer); setAnswer('') }} />
    <TextAction label="Not now" disabled={busy} onPress={() => onAnswer(undefined, true)} />
  </Panel>
}
const a = StyleSheet.create({
  panel: { paddingVertical: 22, gap: 17 },
  status: { flexDirection: 'row', alignItems: 'center', gap: 12, paddingVertical: 5 },
  message: { color: ink, fontSize: 15, lineHeight: 23 },
  code: { color: ink, fontFamily: 'monospace', fontSize: 19, letterSpacing: 1.2, borderWidth: 1, borderColor: edge, borderRadius: 12, padding: 14, backgroundColor: '#ffffffa6', alignSelf: 'flex-start' },
  link: { flexDirection: 'row', alignItems: 'center', gap: 8, minHeight: 44 }, linkText: { color: accent, fontSize: 14 },
  question: { color: ink, fontSize: 15, fontWeight: '500', lineHeight: 23 },
  option: { flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: 15, paddingVertical: 17, borderRadius: 13, borderWidth: 1, borderColor: edge, backgroundColor: '#ffffffa0' },
  selected: { borderColor: '#d9dbde', backgroundColor: '#eff0f2' },
  radio: { width: 19, height: 19, borderWidth: 1, borderColor: '#bec3d0', borderRadius: 10, justifyContent: 'center', alignItems: 'center' },
  dot: { width: 9, height: 9, borderRadius: 5, backgroundColor: accent },
})
