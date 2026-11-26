import { Puzzle } from 'lucide-react'
export function PluginIcon({ icon, large = false }: { icon?: string; large?: boolean }) {
 return <span className={`plugin-mark${large ? ' plugin-mark-large' : ''}`} aria-hidden="true">{icon ? <img src={icon} alt="" /> : <Puzzle size={large ? 30 : 24} strokeWidth={1.65} />}</span>
}
