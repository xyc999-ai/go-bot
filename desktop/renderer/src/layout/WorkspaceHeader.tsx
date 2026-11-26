import type { ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { useLocale } from '../locale'
export function WorkspaceHeader({ title, start, actions }: { title: string; start?: ReactNode; actions?: ReactNode }) {
  const { t } = useLocale()
  return <header className="workspace-header">
    <div className="workspace-header-start">{start ?? <span className="workspace-label">Go Bot</span>}</div>
    <div className="workspace-breadcrumb"><span>Go Bot</span><ChevronRight size={13} /><strong title={title}>{t(title)}</strong></div>
    <div className="workspace-header-actions">{actions}</div>
  </header>
}
