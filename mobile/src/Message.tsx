import { useLocale } from './locale'
import { useState } from 'react'
import { ActivityIndicator, Image, Pressable, StyleSheet, Text, View } from 'react-native'
import { MarkdownText } from './MarkdownText'
import { ChatVideo } from './ChatVideo'
import { ChevronDown, CircleCheck, CircleX, Layers2 } from 'lucide-react-native'
import { groupDisplayBlocks, messageBlocksForDisplay, type Message as Item, type ToolBlock, type Transcript } from '../../frontend/src/chat/transcript'
import { Controller } from './api'
import { FileTile, Images, ImageTools, type Preview } from './MessageMedia'
export type { Preview } from './MessageMedia'
import { accent, edge, glass, Hint, ink, logo, muted, s, userBubble } from './messageTheme'

export function MessageView({ api, id, message, results, openImage, generating = false }: { api: Controller; id: string; message: Item; results: Transcript['toolResults']; openImage: (image: Preview) => void; generating?: boolean }) {
  const { t } = useLocale()
  if (message.role === 'notice') return <View style={styles.notice}><Hint>{message.blocks.map(block => block.type === 'text' ? t(block.text) : '').join(' ')}</Hint></View>
  const user = message.role === 'user'
  return <View style={[styles.message, user && { alignItems: 'flex-end' }]}>
    {!user && <View style={[s.row, { minHeight: 34 }]}>
      <Image source={logo} style={styles.avatar} accessibilityLabel="Go Bot" />
      {generating && <><ActivityIndicator size="small" color={accent} /><Text accessibilityLiveRegion="polite" style={s.caption}>{t('生成中')}</Text></>}
    </View>}
    <View style={[styles.content, user && styles.bubble]}>
      {groupDisplayBlocks(messageBlocksForDisplay(message), results).map(block => {
        if (block.type === 'text') return <View key={block.id}><MarkdownText text={block.text} /></View>
        if (block.type === 'video') return block.file.source === 'presented' && <ChatVideo key={block.id} api={api} sessionId={id} path={block.file.path} name={block.name} description={block.description} />
        if (block.type === 'file') {
          const delivery = block.file.source === 'presented'
            ? { account: api.profile.account, path: block.file.path }
            : undefined
          return <FileTile key={block.id} delivery={delivery} preview={{ uri: api.fileURL(id, block.file, block.name), headers: api.headers, name: block.name || t('附件') }} />
        }
        if (block.type === 'tool') return <Tools key={block.id} tools={[block]} results={results} />
        if (block.type === 'tool-group') return <Tools key={block.id} tools={block.tools} results={results} />
        if (block.type === 'image-tools') return <ImageTools key={block.id} tools={block.tools} images={block.images} results={results} api={api} sessionId={id} openImage={openImage} />
        return <Images key={block.id} images={block.images} api={api} sessionId={id} openImage={openImage} />
      })}
    </View>
  </View>
}

function Tools({ tools, results }: { tools: ToolBlock[]; results: Transcript['toolResults'] }) {
  const { t, language } = useLocale()
  const [expanded, setExpanded] = useState(false)
  return <View style={styles.tools}>
    <Pressable accessibilityRole="button" accessibilityState={{ expanded }} style={styles.toolHeader} onPress={() => setExpanded(old => !old)}>
      <Layers2 color="#7864b7" size={19} strokeWidth={1.8} />
      <Text numberOfLines={1} style={[s.caption, { flex: 1 }]}>{tools.length > 1 ? tools.length + (language === 'en' ? ' ' : ' 个') + t('工具调用') : tools[0].name}</Text>
      <ChevronDown color={muted} size={16} style={{ transform: [{ rotate: expanded ? '180deg' : '0deg' }] }} />
    </Pressable>
    {expanded && tools.map(tool => {
      const result = results[tool.callId]
      return <View key={tool.id} style={styles.toolRow}>
        {result?.status === 'succeeded' ? <CircleCheck size={15} color={accent} /> : result?.status ? <CircleX size={15} color="#9d5e5b" /> : <ActivityIndicator size="small" color={accent} />}
        <Text selectable style={[s.caption, { flex: 1 }]}>{tool.name}{result?.code ? ' · ' + result.code : ''}</Text>
      </View>
    })}
  </View>
}

const styles = StyleSheet.create({
  notice: { alignItems: 'center', paddingVertical: 8 },
  message: { gap: 4 },
  avatar: { width: 36, height: 36 },
  content: { flexShrink: 1, minWidth: 0, gap: 8 },
  bubble: { maxWidth: '83%', borderRadius: 20, backgroundColor: userBubble, paddingHorizontal: 14, paddingVertical: 6 },
  tools: { alignSelf: 'stretch', backgroundColor: glass, borderRadius: 14, borderColor: edge, borderWidth: 1, overflow: 'hidden' },
  toolHeader: { minHeight: 46, flexDirection: 'row', alignItems: 'center', paddingHorizontal: 12, gap: 8 },
  toolRow: { minHeight: 38, paddingHorizontal: 12, flexDirection: 'row', alignItems: 'center', gap: 8, borderTopColor: edge, borderTopWidth: StyleSheet.hairlineWidth },
})
