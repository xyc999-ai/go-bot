import { useEffect, useRef, useState } from 'react'
import { ActivityIndicator, Pressable, StyleSheet, Text, View, BackHandler } from 'react-native'
import { ChevronRight, KeyRound, MoreHorizontal, Plus, Puzzle, RefreshCw, Trash2 } from 'lucide-react-native'
import { useLocale } from '../locale'
import { accent, Button, edge, Hint, IconButton, ink, muted, Panel, s, Sheet, SheetScroll, type PageChrome } from '../ui'
import { installPackage, loadPlugins, removePackage, restartPlugins, setPluginEnabled, updatePackage } from './api'
import { confirmChange, Empty, Fact, Feedback, Fold, Loading, State, TextAction, Toggle, Toolbar, useManagement, x, type ScreenProps } from './shared'
import { Authorizations, usePluginAuthorizations } from './Authorizations'
import { localize } from '../../../frontend/src/extensions/catalog'
import Markdown from 'react-native-markdown-display'
import { Linking } from 'react-native'
import { PluginTutorial } from './PluginTutorial'
import { PluginIcon } from './PluginIcon'

export function Plugins(props: ScreenProps & PageChrome & { embedded?: boolean; active?: boolean; onExit?: () => void }) {
 const { api } = props, { t, language } = useLocale(), m = useManagement(props, loadPlugins)
 const [tab, setTab] = useState<'market' | 'installed'>('market'), [selected, setSelected] = useState<string>(), [authorization, setAuthorization] = useState(false), [menu, setMenu] = useState<string>()
 const listOffset = useRef(0)
 const selectedBusy = !!selected && m.busy.endsWith(':' + selected)
 const busyName = m.view?.catalog.find(row => m.busy.endsWith(':' + row.package))?.name
 const view = m.view, info = view?.status.packages.find(pkg => pkg.name === selected), saved = selected ? view?.settings.packages[selected] : undefined
 const entry = view?.catalog.find(item => item.package === selected), actual = view?.status.instances.find(row => row.package === selected)
 const preparing = ['installing', 'loading'].includes(actual?.state ?? '')
 const connections = usePluginAuthorizations(props, actual?.state === 'ready' && saved?.enabled ? [actual.id] : [])
 const names = Object.keys(view?.settings.packages ?? {}).sort(), title = entry ? entry.name : info?.title || selected || ''
 const install = async (name: string) => {
  const release = view?.catalog.find(row => row.package === name)
  if (release) await m.run('install:' + name, () => installPackage(api, release.pluginId), 'Plugin and dependencies installed. Enable it to use its capabilities.')
 }
 useEffect(() => {
  if (!props.active) return
  const listener = BackHandler.addEventListener('hardwareBackPress', () => { if (authorization) setAuthorization(false); else if (selected) { setSelected(undefined); setTab('market') } else if (props.onExit) props.onExit(); else return false; return true })
  return () => listener.remove()
 }, [props.active, selected, authorization, props.onExit])
 return <>
 <SheetScroll key={authorization ? 'authorization' : selected ?? tab} embedded={props.embedded} scrollOffset={!selected ? listOffset : undefined} bottomInset={props.bottomInset} header={!selected ? props.header : undefined} navigation={!selected ? props.navigation : undefined}>
  {authorization ? <Authorizations connections={connections} onBusyChange={props.onBusyChange} onBack={() => setAuthorization(false)} />
   : selected && view ? <>
    <Toolbar title="Plugin details" onBack={() => { setSelected(undefined); setTab('market') }}>{saved && <IconButton label={t('More options')} disabled={!!m.busy || ['installing', 'loading'].includes(actual?.state ?? '')} onPress={() => setMenu(selected)}><MoreHorizontal size={20} color={muted} /></IconButton>}</Toolbar>
    <View style={market.detailHead}><PluginIcon api={api} icon={entry?.icon} large /><View style={{flex:1,gap:8}}><Text style={market.detailTitle}>{title}</Text><Text style={s.hint}>{entry ? localize(entry.description, language).split('\n')[0] : info?.description}</Text></View></View>
    {entry && localize(entry.description,language).includes('\n') && <Panel style={{paddingVertical:16}}><Fold label="Connection setup"><Markdown style={{body:{fontSize:14,lineHeight:23,color:muted},link:{color:accent},paragraph:{marginTop:10,marginBottom:6}}} onLinkPress={url=>{if(/^https?:\/\//i.test(url))void Linking.openURL(url).catch(()=>{});return false}}>{localize(entry.description,language).split('\n').slice(1).join('\n').trim()}</Markdown></Fold></Panel>}
    {entry && <PluginTutorial api={api} tutorials={entry.tutorials} />}
    <Feedback error={m.error} notice={m.notice} />
    {m.busy && <View style={s.row}><ActivityIndicator size="small" color={accent} /><Text style={s.meta}>{busyName ? busyName + ' · ' : ''}{t(m.busy.startsWith('install:') ? 'Installing…' : 'Applying plugin changes…')}</Text></View>}
    {info?.availableVersion && entry && <Panel><View style={s.row}><View style={{flex:1,gap:6}}><Text style={s.caption}>{t('Update available')}</Text><Text style={s.meta}>{info.version} → {info.availableVersion}</Text></View><Button label={t('Update plugin')} busy={!!m.busy} onPress={() => { void m.run('install:' + selected, () => updatePackage(api, selected, entry.pluginId)) }} /></View></Panel>}
    {!saved ? <Button primary busy={selectedBusy} disabled={!!m.busy} label={t('Install plugin')} onPress={() => { void install(selected) }}><Plus size={18} color="#fff" /></Button> : <Panel style={{paddingVertical:20}}><View style={[s.row,{justifyContent:'space-between'}]}><Text style={s.meta}>{t('Plugin status')}</Text>{!selectedBusy && <State value={actual?.state ?? (saved.enabled ? 'unavailable' : 'disabled')} />}</View><Toggle label="Enable plugin" checked={saved.enabled} disabled={!!m.busy || preparing} onChange={enabled => { void m.run('settings:' + selected, () => setPluginEnabled(api, selected, enabled)) }} />{!selectedBusy && <Hint error>{actual?.error || view.status.error}</Hint>}</Panel>}
    {entry && <>
     <View style={{gap:15,paddingVertical:8}}><Fact label="Version" value={info?.present ? info.version : entry.version} /><Fact label="Maintainer" value={entry.maintainer} /><Fact label="License" value={entry.license} /></View>

    </>}
    {(connections.entries.length > 0 || connections.mcp.servers.length > 0) && <Button label={t('Accounts and authorization')} disabled={!!m.busy || view.status.runtime !== 'running'} onPress={() => setAuthorization(true)}><KeyRound size={18} color={accent} /></Button>}
    {(connections.entries.length > 0 || connections.mcp.servers.length > 0) && <Hint error>{t(connections.state.error || connections.mcp.error)}</Hint>}
   </> : <>
    <View style={s.row}><Text style={[s.hint,{flex:1}]}>{t('Extend your agent with plugins reviewed for this project.')}</Text><IconButton label={t('Refresh extensions')} onPress={m.reload}><RefreshCw size={18} color={muted} /></IconButton></View>
    <View style={market.tabs}>{(['market','installed'] as const).map(value => <Pressable key={value} accessibilityRole="tab" accessibilityState={{selected:tab === value}} onPress={() => setTab(value)} style={[market.tab,tab === value && market.selected]}><Text style={[market.label,tab === value && market.selectedLabel]}>{t(value === 'market' ? 'Plugin market' : 'Installed')}{value === 'installed' ? ` · ${names.length}` : ''}</Text></Pressable>)}</View>
    <Feedback error={m.error} notice={m.notice} />
    {m.busy && <View style={s.row}><ActivityIndicator size="small" color={accent} /><Text style={[s.hint,{flex:1}]}>{busyName ? busyName + ' · ' : ''}{t('Plugin changes continue while you use other features.')}</Text></View>}
    {!view && m.loading && <Loading />}
    {view && <>
     {!(tab === 'market' ? view.catalog.length : names.length) && <Empty title={tab === 'market' ? 'More capabilities are on the way' : 'No plugins installed'} text="Your built-in agent works without plugins. Add packages when you need more capabilities."><Puzzle size={30} color={muted} strokeWidth={1.4} /></Empty>}
     <View style={{gap:8}}>{(tab === 'market' ? view.catalog.map(entry => entry.package) : names).map(name => {
      const release = view.catalog.find(row => row.package === name), saved = view.settings.packages[name], state = view.status.instances.find(row => row.package === name)
      const label = release ? release.name : view.status.packages.find(pkg => pkg.name === name)?.title || name
      return <View key={name} style={market.tile}>
       <Pressable accessibilityRole="button" accessibilityLabel={label} onPress={() => setSelected(name)} style={({pressed}) => [market.tileMain,pressed && s.pressed]}><PluginIcon api={api} icon={release?.icon} /><View style={{flex:1,gap:5}}><Text style={market.title}>{label}</Text><Text numberOfLines={2} style={market.summary}>{release ? localize(release.description,language).split('\n')[0] : name}</Text>{view.status.packages.find(row => row.name === name)?.availableVersion && <Text style={market.update}>{t('Update available')}</Text>}{(tab === 'installed' || ['installing', 'installation-failed'].includes(state?.state ?? '')) && <State value={state?.state ?? (saved?.enabled ? 'unavailable' : 'disabled')} />}</View></Pressable>
       {m.busy === 'install:' + name || state?.state === 'installing' ? <ActivityIndicator style={{width:44}} color={accent} /> : <IconButton label={t(saved ? 'More options' : 'Install plugin') + ': ' + label} disabled={!!m.busy || state?.state === 'loading'} onPress={() => { if (saved) setMenu(name); else void install(name) }}>{saved ? <MoreHorizontal size={20} color={muted} /> : <Plus size={22} color={muted} />}</IconButton>}
      </View>
     })}</View>
     {tab === 'installed' && <Fold label="Plugin runtime"><View style={s.row}><State value={view.status.runtime} /><TextAction label="Restart" disabled={!!m.busy} onPress={() => { void m.run('restart',() => restartPlugins(api)) }} /></View><Hint error>{view.status.error}</Hint></Fold>}
    </>}
   </>}
 </SheetScroll>
 {menu && <Sheet title={t('More options')} onClose={() => setMenu(undefined)} raised={18}><SheetScroll>
  {!selected && <TextAction label="Plugin details" onPress={() => { setSelected(menu);setMenu(undefined) }} />}
  <TextAction label="Reinstall plugin" onPress={() => { const name=menu, release=view?.catalog.find(row=>row.package===name);setMenu(undefined);if(release) void m.run('install:'+name,()=>updatePackage(api,name,release.pluginId)) }} />
  <TextAction label="Uninstall" destructive onPress={() => { const name=menu;setMenu(undefined);void confirmChange('Uninstall this package? Its credentials and workspace files will stay on disk.',t).then(async ok=>{if(ok && await m.run('remove:'+name,()=>removePackage(api,name)))setSelected(undefined)}) }} />
 </SheetScroll></Sheet>}
 </>
}
const market = StyleSheet.create({
 tabs:{flexDirection:'row',gap:26,borderBottomWidth:StyleSheet.hairlineWidth,borderBottomColor:edge},
 tab:{alignItems:'center',justifyContent:'center',minHeight:44,paddingHorizontal:2,borderBottomWidth:2,borderBottomColor:'transparent'},
 selected:{borderBottomColor:ink},label:{color:muted,fontSize:14,fontWeight:'500'},selectedLabel:{color:accent},
 tile:{flexDirection:'row',alignItems:'center',gap:4,borderRadius:17},
 tileMain:{flex:1,flexDirection:'row',alignItems:'center',gap:15,paddingVertical:16},
 title:{color:ink,fontSize:16,fontWeight:'500',letterSpacing:-.2},summary:{color:muted,fontSize:13,lineHeight:20},
 detailHead:{flexDirection:'row',alignItems:'flex-start',gap:18,paddingVertical:14},detailTitle:{fontSize:25,color:ink,fontWeight:'600',letterSpacing:-.6},
 update:{fontSize:11,color:'#3e6a52',fontWeight:'600'},
 section:{gap:16,paddingVertical:24,borderBottomWidth:1,borderBottomColor:'#e9eaec'},heading:{color:ink,fontWeight:'600',fontSize:14},
})
