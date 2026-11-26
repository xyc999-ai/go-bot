import {useState} from 'react'
import {PlayCircle} from 'lucide-react'
import type {PluginTutorial as Tutorial} from '../api/extensions'
import {useLocale} from '../locale'
import {selectTutorial} from './catalog'
export function PluginTutorial({tutorials}:{tutorials?:Tutorial[]}) {
 const {language,t}=useLocale(),tutorial=selectTutorial(tutorials,language)
 const [playing,setPlaying]=useState(false),[error,setError]=useState(false)
 if(!tutorial)return null
 return <section className="plugin-tutorial"><div className="plugin-tutorial-heading"><h2>{t('Video guide')}</h2><p>{tutorial.title}</p></div>
  {playing ? <video key={tutorial.video} controls playsInline autoPlay preload="metadata" poster={tutorial.poster} onError={()=>setError(true)} src={tutorial.video} /> : <button className="plugin-tutorial-preview" type="button" onClick={()=>{setError(false);setPlaying(true)}} aria-label={t('Play video guide')}>
   {tutorial.poster && <img src={tutorial.poster} alt="" />}<span><PlayCircle size={42} strokeWidth={1.5} />{t('Play video guide')}</span></button>}
  {error && <p role="alert" className="extension-diagnostic">{t('Could not load this video. Try again.')}</p>}
 </section>
}
