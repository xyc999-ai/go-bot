import { useEffect, useRef, useState } from 'react'
import { Pressable, StyleSheet, Text, View } from 'react-native'
import { ChevronDown, ChevronRight, Folder, RefreshCw } from 'lucide-react-native'
import { type Controller, type HostItems } from './api'
import { formatSize } from './library'
import { FileIcon } from './FileIcon'
import { useLocale } from './locale'
import { accent, Button, edge, Hint, IconButton, muted, Panel, s, Sheet, SheetScroll } from './ui'

export function HostFilesSheet({ api, close }: { api: Controller; close: () => void }) {
  const { t } = useLocale()
  const [version, setVersion] = useState(0)
  const [root, setRoot] = useState('/root/harness')
  return <Sheet title={t('文件系统')} onClose={close}><SheetScroll>
    <Panel style={styles.panel}>
      <View style={styles.root}>
        <Folder size={18} color={accent} />
        <Text selectable style={[s.caption, { flex: 1 }]}>{root}</Text>
        <IconButton label={t('刷新')} onPress={() => setVersion(v => v + 1)}><RefreshCw size={17} color={accent} /></IconButton>
      </View>
      <DirectoryListing api={api} path="" depth={0} version={version} onRoot={setRoot} />
    </Panel>
  </SheetScroll></Sheet>
}

function DirectoryListing({ api, path, depth, version, onRoot }: {
  api: Controller; path: string; depth: number; version: number; onRoot?: (path: string) => void
}) {
  const { t } = useLocale()
  const [page, setPage] = useState<HostItems>()
  const [expanded, setExpanded] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [failedOffset, setFailedOffset] = useState<number>()
  const request = useRef<AbortController | undefined>(undefined)
  const lastOffset = useRef(0)

  async function load(offset: number, through = offset) {
    const firstOffset = offset
    request.current?.abort()
    const controller = new AbortController()
    request.current = controller
    setLoading(true)
    setFailedOffset(undefined)
    const timeout = setTimeout(() => controller.abort(), 20000)
    try {
      let next = await api.hostFiles(path, offset, controller.signal)
      const files = [...next.files]
      while (next.nextOffset !== undefined && next.nextOffset > offset && next.nextOffset <= through) {
        offset = next.nextOffset
        next = await api.hostFiles(path, offset, controller.signal)
        files.push(...next.files)
      }
      if (request.current !== controller) return
      lastOffset.current = offset
      if (next.rootPath) onRoot?.(next.rootPath)
      setPage(previous => ({ ...next, files: firstOffset > 0 && previous
        ? [...new Map([...previous.files, ...files].map(item => [item.path, item])).values()] : files }))
    } catch {
      if (request.current === controller) setFailedOffset(firstOffset)
    } finally {
      clearTimeout(timeout)
      if (request.current === controller) {
        request.current = undefined
        setLoading(false)
      }
    }
  }
  useEffect(() => {
    void load(0, lastOffset.current)
    return () => {
      request.current?.abort()
      request.current = undefined
    }
  }, [api, path, version])

  const indent = Math.min(depth, 6) * 13
  const files = [...(page?.files ?? [])].sort((a, b) => Number(b.isDir) - Number(a.isDir) || a.name.localeCompare(b.name))
  return <>
    {files.map(item => {
      const open = expanded.includes(item.path)
      const Chevron = open ? ChevronDown : ChevronRight
      const row = [styles.row, { paddingLeft: 10 + indent }]
      if (!item.isDir) return <View key={item.path} style={[row, { paddingLeft: 31 + indent }]}>
        <FileIcon name={item.name} size={25} />
        <Text numberOfLines={1} style={styles.name}>{item.name}</Text>
        <Text style={s.meta}>{formatSize(item.size)}</Text>
      </View>
      return <View key={item.path}>
        <Pressable accessibilityRole="button" accessibilityState={{ expanded: open }} accessibilityLabel={item.name}
          onPress={() => setExpanded(old => open ? old.filter(entry => entry !== item.path) : [...old, item.path])}
          style={({ pressed }) => [row, (open || pressed) && styles.open]}>
          <Chevron size={15} color={muted} />
          <Folder size={19} color={accent} />
          <Text numberOfLines={1} style={styles.name}>{item.name}</Text>
        </Pressable>
        {open && <DirectoryListing api={api} path={item.path} version={version} depth={depth + 1} />}
      </View>
    })}
    <View style={{ paddingLeft: 13 + indent, paddingRight: 12 }}>
      {loading && <Hint>{t('正在读取目录…')}</Hint>}
      {failedOffset !== undefined && <><Hint error>{t('无法读取目录。')}</Hint><Button label={t('重新加载')} onPress={() => load(failedOffset, failedOffset || lastOffset.current)} /></>}
      {!loading && failedOffset === undefined && !files.length && <Hint>{t('此目录为空。')}</Hint>}
      {!loading && failedOffset === undefined && page?.nextOffset !== undefined && <Button label={t('加载更多')} onPress={() => load(page.nextOffset!)} />}
    </View>
  </>
}

const styles = StyleSheet.create({
  panel: { paddingHorizontal: 7, paddingVertical: 6, gap: 0 },
  root: { ...s.row, minHeight: 46, paddingLeft: 12, paddingRight: 4, borderBottomColor: edge, borderBottomWidth: StyleSheet.hairlineWidth },
  row: { ...s.row, minHeight: 44, paddingRight: 12, gap: 7, borderRadius: 10 },
  open: { backgroundColor: '#eff0f2' },
  name: { ...s.body, flex: 1, minWidth: 0 },
})
