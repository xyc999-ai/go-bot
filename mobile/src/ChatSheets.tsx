import { modelProtocol, protocolNames } from '../../frontend/src/settings/modelProtocol'
import { useLocale } from './locale'
import { useEffect, useState } from 'react'
import { Alert, Image, Pressable, StyleSheet, Text, View } from 'react-native'
import { Check, Plus, Share2, Trash2 } from 'lucide-react-native'
import { Controller, problem, type ModelSelection, type ModelSettings } from './api'
import type { Answer, PendingQuestion } from '../../frontend/src/chat/transcript'
import { FileIcon } from './FileIcon'
import { deleteLibrarySelection, formatSize, pickFiles, pickPhotos, readLibrary, shareLibrarySelection, subscribeLibrary, type Kind, type LibraryEntry } from './library'
import { accent, Button, Field, edge, glass, Hint, IconButton, ink, muted, Panel, s, Sheet, SheetScroll, wash } from './ui'

export function ModelSheet({ api, choice, close, choose, settingsOpen }: {
  api: Controller; choice?: ModelSelection | null; close: () => void; choose: (choice: ModelSelection) => Promise<void>; settingsOpen: () => void
}) {
  const { t } = useLocale()
  const [data, setData] = useState<ModelSettings>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    api.modelSettings().then(setData).catch(e => setError(problem(e, '无法读取模型列表。')))
  }, [api])
  const selected = choice ?? { endpointId: data?.defaultEndpointId ?? '', modelId: data?.endpoints[data.defaultEndpointId]?.models[0] }
  return <Sheet title={t('选择模型')} onClose={close}>{afterClose => <SheetScroll>
    {data && !Object.keys(data.endpoints).length && <Hint>{t('请先添加端点并设置默认模型。')}</Hint>}
    {Object.entries(data?.endpoints ?? {}).map(([endpointId, endpoint]) => <View key={endpointId} style={{ gap: 6 }}>
      <Text style={s.caption}>{endpoint.name} · {protocolNames[modelProtocol(endpoint.protocol)]}</Text>
      {endpoint.models.map(modelId => {
        const active = endpointId === selected.endpointId && modelId === selected.modelId
        return <Pressable key={modelId} accessibilityRole="button" accessibilityState={{ selected: active }} disabled={busy} onPress={async () => {
          setBusy(true)
          setError('')
          try { await choose({ endpointId, modelId }); close() }
          catch (e) { setError(problem(e, '无法切换模型。')) }
          finally { setBusy(false) }
        }} style={[s.row, { minHeight: 48, paddingHorizontal: 13, borderRadius: 13, backgroundColor: active ? wash : glass }]}>
          <Text numberOfLines={2} style={[s.body, { flex: 1 }]}>{modelId}</Text>{active && <Check size={17} color={accent} />}
        </Pressable>
      })}
    </View>)}
    <Button label={t('打开模型设置')} onPress={() => afterClose(settingsOpen)} />
    <Hint error>{t(error)}</Hint>
  </SheetScroll>}</Sheet>
}

export function LibrarySheet({ kind, close, add, remove }: {
  kind: Kind; close: () => void; add: (items: LibraryEntry[]) => void; remove: (ids: string[]) => void
}) {
  const { t, language } = useLocale()
  const [items, setItems] = useState(() => readLibrary(kind))
  useEffect(() => {
    const refresh = () => setItems(readLibrary(kind))
    const unsubscribe = subscribeLibrary(kind, refresh)
    refresh()
    return unsubscribe
  }, [kind])
  const [selected, setSelected] = useState<string[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function importItems() {
    setBusy(true)
    setError('')
    try {
      const imported = await (kind === 'image' ? pickPhotos() : pickFiles())
      setItems(readLibrary(kind))
      setSelected(previous => [...previous, ...imported.filter(item => item.kind === kind).map(item => item.id)])
      if (imported.some(item => item.kind !== kind)) setError('图片已存入本地图片库。')
    } catch { setError('导入失败，请重试。') } finally { setBusy(false) }
  }
  const selection = selected.flatMap(id => items.find(item => item.id === id) ?? [])
  const title = t(kind === 'image' ? '本地图片' : '本地文件')

  async function share() {
    setBusy(true)
    setError('')
    try { await shareLibrarySelection(selection) }
    catch { setError('无法分享所选项目，请重试。') }
    finally { setBusy(false) }
  }

  function confirmDelete() {
    Alert.alert(t('删除所选项目？'), t('将从 App 的本地库中删除所选项目。'), [
      { text: t('取消'), style: 'cancel' },
      { text: t('删除'), style: 'destructive', onPress: () => {
        const { deleted, failed } = deleteLibrarySelection(selection)
        remove(deleted)
        setSelected(previous => previous.filter(id => !deleted.includes(id)))
        setError(failed ? '部分项目无法删除，请重试。' : '')
      } },
    ])
  }

  return <Sheet title={title} onClose={close} headerAction={<IconButton style={{ backgroundColor: glass, borderWidth: 1, borderColor: '#fff' }}
      label={t(kind === 'image' ? '从图库导入' : '从设备导入')} disabled={busy} onPress={importItems}><Plus size={22} color={ink} /></IconButton>
  }><>
    <SheetScroll>
      {!items.length && <Hint>{t(kind === 'image' ? '还没有导入图片。' : '还没有导入文件。')}</Hint>}
      <View style={kind === 'image' ? { flexDirection: 'row', flexWrap: 'wrap', gap: 8 } : { gap: 6 }}>
        {items.map(item => {
          const active = selected.includes(item.id)
          return <Pressable key={item.id} accessibilityRole="button" accessibilityLabel={item.name} accessibilityState={{ selected: active }} disabled={busy}
            onPress={() => setSelected(previous => active ? previous.filter(id => id !== item.id) : [...previous, item.id])}
            style={kind === 'image' ? { width: '31.6%', aspectRatio: 1, overflow: 'hidden', borderRadius: 12, borderWidth: 2, borderColor: active ? '#33363a' : '#fff' }
              : [s.row, { minHeight: 66, borderRadius: 15, paddingHorizontal: 12, paddingVertical: 9, backgroundColor: glass, borderWidth: 1, borderColor: active ? '#33363a' : edge }]}>
            {kind === 'image'
              ? <Image source={{ uri: item.uri }} style={{ width: '100%', height: '100%' }} resizeMode="cover" />
              : <><FileIcon name={item.name} /><View style={{ flex: 1, gap: 2 }}>
                <Text numberOfLines={1} style={s.body}>{item.name}</Text>
                <Text style={s.meta}>{formatSize(item.size)}{item.savedAt > 0 && ' · ' + new Date(item.savedAt).toLocaleString(language)}</Text>
              </View></>}
            {(kind === 'file' || active) && <View style={[{ width: 20, height: 20, alignItems: 'center', justifyContent: 'center', borderWidth: 1,
              borderColor: active ? '#33363a' : '#c8cbd0', backgroundColor: active ? '#33363a' : 'transparent', borderRadius: 5 },
              kind === 'image' && { position: 'absolute', right: 4, top: 4, borderRadius: 10, borderColor: '#fff' }]}>
              {active && <Check size={13} color="#fff" strokeWidth={2.5} />}
            </View>}
          </Pressable>
        })}
      </View>
    </SheetScroll>
    {!!selection.length && <View style={{ flexDirection: 'row', alignItems: 'center', gap: 8, paddingTop: 6, borderTopWidth: StyleSheet.hairlineWidth, borderColor: edge }}>
      <View style={{ flex: 1 }}>
        <Button label={t('添加到输入框') + ' · ' + selection.length} soft disabled={busy} onPress={() => {
          try { add(selection); close() }
          catch (e) { setError(e instanceof Error ? e.message : '无法添加附件。') }
        }} />
      </View>
      <Button label={t('分享')} disabled={busy} onPress={share}><Share2 size={17} color={accent} /></Button>
      <Button label={t('删除')} disabled={busy} onPress={confirmDelete}><Trash2 size={16} color={muted} /></Button>
    </View>}
    <Hint>{t(error)}</Hint>
  </></Sheet>
}

export function QuestionSheet({ pending, close, submit, cancel }: {
  pending: PendingQuestion; close: () => void; submit: (answers: Answer[]) => Promise<unknown>; cancel: () => Promise<unknown>
}) {
  const { t } = useLocale()
  const [answers, setAnswers] = useState<Record<string, Answer>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const complete = pending.questions.every(question => answers[question.id]?.selected.length || answers[question.id]?.custom?.trim())
  async function run(work: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try { await work(); close() }
    catch (e) { setError(problem(e, '无法提交，请重试。')) }
    finally { setBusy(false) }
  }
  return <Sheet title={t(pending.questions.some(question => question.id === 'plan-review') ? '审阅方案' : '助手需要你的确认')} onClose={close} raised={64}><SheetScroll>
    {pending.questions.map(question => {
      const a = answers[question.id] ?? { id: question.id, selected: [] }
      return <View key={question.id} style={{ gap: 8 }}>
        {!!question.header && <Text style={s.caption}>{question.header}</Text>}
        <Text style={[s.body, { fontWeight: '600' }]}>{t(question.question)}</Text>
        <Hint>{question.detail}</Hint>
        {question.options?.map(option => {
          const active = a.selected.includes(option.label)
          return <Pressable accessibilityRole={question.multiSelect ? 'checkbox' : 'radio'} accessibilityState={{ checked: active }}
            key={option.label} style={[s.row, { padding: 10, minHeight: 45, borderRadius: 13, backgroundColor: active ? wash : glass }]} onPress={() => {
              const selected = active ? a.selected.filter(label => label !== option.label) : question.multiSelect ? [...a.selected, option.label] : [option.label]
              setAnswers(previous => ({ ...previous, [question.id]: { ...a, selected, ...(!question.multiSelect && { custom: '' }) } }))
            }}>
            <View style={{ width: 19, height: 19, borderRadius: question.multiSelect ? 5 : 10, borderWidth: 1.5,
              borderColor: active ? accent : '#92969d', alignItems: 'center', justifyContent: 'center' }}>
              {active && (question.multiSelect ? <Check size={12} color={accent} strokeWidth={2.4} />
                : <View style={{ width: 9, height: 9, borderRadius: 5, backgroundColor: accent }} />)}
            </View>
            <View style={{ flex: 1 }}><Text style={{ color: ink, fontSize: 15 }}>{t(option.label)}</Text><Hint>{t(option.description ?? '')}</Hint></View>
          </Pressable>
        })}
        <Field label={t(question.options?.length ? '其他答案' : '你的回答')} multiline style={{ minHeight: 54 }} value={a.custom ?? ''} onChangeText={custom =>
          setAnswers(previous => ({ ...previous, [question.id]: { ...a, custom, ...(!question.multiSelect && { selected: [] }) } }))} />
      </View>
    })}
    <Button primary label={t('提交答案')} busy={busy} disabled={!complete} onPress={() => run(() => submit(pending.questions.map(question => answers[question.id])))} />
    <Button label={t('取消此次提问')} disabled={busy} onPress={() => run(cancel)} />
    <Hint error>{t(error)}</Hint>
  </SheetScroll></Sheet>
}
