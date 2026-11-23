import { Children, cloneElement, isValidElement, memo, useEffect, useRef, useState } from 'react'
import { Linking, Platform, Pressable, ScrollView, StyleSheet, Text, View, type StyleProp, type ViewStyle } from 'react-native'
import Markdown, { MarkdownIt, type ASTNode } from 'react-native-markdown-display'
import * as Clipboard from 'expo-clipboard'
import { Check, Copy } from 'lucide-react-native'
import { useLocale } from './locale'
import { accent, ink, muted } from './ui'

const mono = Platform.OS === 'ios' ? 'Menlo' : 'monospace'
const border = '#e1e3e8', paper = '#f6f7f9'
type SourceNode = ASTNode & { sourceMeta?: { markdownSource?: string }; sourceInfo?: string }
const parser = new MarkdownIt({ typographer: true })
// Keep the original source with the parser token before the library creates AST nodes.
parser.core.ruler.after('block', 'copy-markdown-source', (state: { src: string; tokens: { map: [number, number] | null; type: string; meta?: Record<string, unknown> }[] }) => {
  const lines = state.src.split('\n')
  for (const token of state.tokens) {
    if (token.map && ['table_open', 'fence', 'code_block'].includes(token.type)) {
      token.meta = { ...token.meta, markdownSource: lines.slice(token.map[0], token.map[1]).join('\n').replace(/\n$/, '') }
    }
  }
})

function CopyButton({ raw, kind }: { raw: string; kind: 'table' | 'code' }) {
  const { t } = useLocale()
  const [status, setStatus] = useState<'idle' | 'copied' | 'failed'>('idle')
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const label = status === 'copied' ? '已复制' : status === 'failed' ? '复制失败，重试' : kind === 'table' ? '复制表格' : '复制代码'
  return <Pressable accessibilityRole="button" accessibilityLabel={t(label)} style={({ pressed }) => [styles.copy, pressed && { opacity: .55 }]} onPress={async () => {
    clearTimeout(timer.current)
    try { await Clipboard.setStringAsync(raw); setStatus('copied') } catch { setStatus('failed') }
    timer.current = setTimeout(() => setStatus('idle'), 2200)
  }}>{status === 'copied' ? <Check size={14} color={muted} /> : <Copy size={14} color={muted} />}<Text accessibilityLiveRegion="polite" style={styles.action}>{t(label)}</Text></Pressable>
}

function CodePanel({ node, style }: { node: SourceNode; style?: StyleProp<ViewStyle> }) {
  const { t } = useLocale()
  return <View style={[styles.panel, style]}>
    <View style={styles.toolbar}><Text style={styles.caption}>{node.sourceInfo?.trim().split(/\s/)[0] || t('代码')}</Text><CopyButton raw={node.sourceMeta?.markdownSource ?? node.content} kind="code" /></View>
    <ScrollView horizontal nestedScrollEnabled directionalLockEnabled showsHorizontalScrollIndicator accessibilityLabel={t('代码，可左右滚动')} contentContainerStyle={styles.codeContent}>
      <Text selectable style={styles.code}>{node.content.replace(/\n$/, '')}</Text>
    </ScrollView>
  </View>
}

function rows(node: ASTNode): ASTNode[] { return node.type === 'tr' ? [node] : node.children.flatMap(rows) }
function plain(node: ASTNode): string { return node.type === 'softbreak' || node.type === 'hardbreak' ? '\n' : node.children.length ? node.children.map(plain).join('') : node.content || '' }
function columnWidths(table: ASTNode) {
  const result: number[] = []
  for (const row of rows(table)) row.children.forEach((cell, index) => {
    const length = Array.from(plain(cell)).reduce((width, char) => width + (/[^\x00-\x7f]/.test(char) ? 16 : 8), 0)
    result[index] = Math.max(result[index] || 112, Math.min(240, length + 28))
  })
  return result
}

export const MarkdownText = memo(function MarkdownText({ text }: { text: string }) {
  const { t } = useLocale()
  return <Markdown markdownit={parser} style={markdown} rules={{
    body: (node, children, _parents, nodeStyles) => {
      const blocks = Children.toArray(children)
      return <View key={node.key} style={nodeStyles._VIEW_SAFE_body}>{blocks.map((child, index) =>
        isValidElement<{ style?: StyleProp<ViewStyle> }>(child) ? cloneElement(child, { style: [child.props.style,
          index === 0 && styles.firstBlock, index === blocks.length - 1 && styles.lastBlock] }) : child,
      )}</View>
    },
    image: node => <Text key={node.key} selectable>{'![' + (node.attributes.alt ?? '') + '](' + node.attributes.src + ')'}</Text>,
    fence: node => <CodePanel key={node.key} node={node} />,
    code_block: node => <CodePanel key={node.key} node={node} />,
    table: (node: SourceNode, children) => <View key={node.key} style={[styles.panel, { backgroundColor: '#ffffff80' }]}>
      <View style={styles.toolbar}><Text style={styles.caption}>{t('表格')}</Text><CopyButton raw={node.sourceMeta?.markdownSource || ''} kind="table" /></View>
      <ScrollView horizontal nestedScrollEnabled directionalLockEnabled showsHorizontalScrollIndicator accessibilityLabel={t('表格，可左右滚动')}>
        <View style={{ width: columnWidths(node).reduce((a, b) => a + b, 0) }}>{children}</View>
      </ScrollView>
    </View>,
    th: (node, children, parents) => {
      const table = parents.find(parent => parent.type === 'table')
      return <View key={node.key} style={[styles.cell, styles.headerCell, { width: table ? columnWidths(table)[node.index] : 140 }]}>{children}</View>
    },
    td: (node, children, parents) => {
      const table = parents.find(parent => parent.type === 'table')
      return <View key={node.key} style={[styles.cell, { width: table ? columnWidths(table)[node.index] : 140 }]}>{children}</View>
    },
  }} onLinkPress={url => { if (/^https?:\/\//i.test(url)) void Linking.openURL(url).catch(() => {}); return false }}>{text}</Markdown>
})

const styles = StyleSheet.create({
  panel: { alignSelf: 'stretch', minWidth: 0, marginTop: 8, marginBottom: 18, borderWidth: StyleSheet.hairlineWidth, borderColor: border, borderRadius: 10, backgroundColor: paper, overflow: 'hidden' },
  toolbar: { minHeight: 44, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', paddingLeft: 14, paddingRight: 6, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: border, backgroundColor: '#f4f5f8' },
  firstBlock: { marginTop: 0 }, lastBlock: { marginBottom: 0 },
  caption: { fontSize: 11, fontWeight: '500', color: muted },
  copy: { minHeight: 44, minWidth: 72, paddingHorizontal: 8, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 6 },
  action: { fontSize: 11, color: muted },
  codeContent: { padding: 15, flexGrow: 1 },
  code: { fontFamily: mono, fontSize: 13, lineHeight: 22, color: '#313747', flexShrink: 0 },
  cell: { paddingHorizontal: 14, paddingVertical: 12, justifyContent: 'center', flexGrow: 0, flexShrink: 0 },
  headerCell: { backgroundColor: '#f4f5f8' },
})
const markdown = StyleSheet.create({
  body: { color: ink, fontSize: 16, lineHeight: 27 },
  paragraph: { marginTop: 0, marginBottom: 16 },
  heading1: { fontSize: 25, lineHeight: 33, fontWeight: '700', marginTop: 24, marginBottom: 14 },
  heading2: { fontSize: 22, lineHeight: 30, fontWeight: '700', marginTop: 22, marginBottom: 12 },
  heading3: { fontSize: 19, lineHeight: 27, fontWeight: '600', marginTop: 20, marginBottom: 12 },
  heading4: { fontSize: 17, lineHeight: 26, fontWeight: '600', marginTop: 18, marginBottom: 10 },
  heading5: { fontSize: 16, lineHeight: 25, fontWeight: '600', marginTop: 18, marginBottom: 10 },
  heading6: { fontSize: 15, lineHeight: 24, fontWeight: '600', marginTop: 16, marginBottom: 8 },
  strong: { fontWeight: '600' },
  bullet_list: { marginBottom: 12 }, ordered_list: { marginBottom: 12 },
  list_item: { marginBottom: 6 },
  link: { color: accent, textDecorationLine: 'underline' },
  code_inline: { fontFamily: mono, fontSize: 14, fontWeight: '400', color: '#3f414a', backgroundColor: '#f1f2f5', borderWidth: 0, padding: 0, borderRadius: 0 },
  blockquote: { marginTop: 6, marginBottom: 18, paddingHorizontal: 15, paddingTop: 12, paddingBottom: 0, borderLeftWidth: 3, borderColor: '#d4d7e0', backgroundColor: '#f5f6f980' },
  tr: { flexDirection: 'row', borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: border },
  thead: { backgroundColor: '#f4f5f8' },
  th: { fontSize: 14, lineHeight: 22, fontWeight: '600' },
  td: { fontSize: 14, lineHeight: 22 },
  text: { color: ink },
  hr: { height: StyleSheet.hairlineWidth, backgroundColor: border, marginVertical: 22 },
})
