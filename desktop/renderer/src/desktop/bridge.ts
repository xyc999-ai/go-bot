export type DesktopCommand = { type: 'new-chat' | 'resume' | 'navigate'; path?: string }
import type { ClientIdentity } from '../clients/identity'
export type Configuration = { controller: string; version: string; identity: ClientIdentity }
let device: ClientIdentity
export function setDesktopIdentity(value: ClientIdentity) { device = value }
export function desktopClientIdentity(): ClientIdentity { return device }

let configuredOrigin = ''
export function setControllerOrigin(value: string) { configuredOrigin = value }
export function controllerOrigin() { return configuredOrigin }

declare global {
  interface Window {
    desktop: {
      platform: string
      setLanguage(value: 'en' | 'zh-CN'): Promise<void>
      configuration(): Promise<Configuration>
      notify(value: { sessionId: string; kind: 'approval' | 'question' | 'completed' }): Promise<boolean>
      onCommand(listener: (command: DesktopCommand) => void): () => void
    }
  }
}

export function notifyHostEvent(kind: string, data: unknown) {
  if (kind !== 'host' || !data || typeof data !== 'object') return
  const event = data as { sessionId?: string; type?: string; data?: { type?: string; questions?: { approval?: unknown }[] } }
  if (!event.sessionId) return
  const questions = event.data?.questions
  const notification = event.type === 'questions.state' && questions?.length
    ? questions.some(question => question.approval) ? 'approval' : 'question'
    : event.type === 'session' && event.data?.type === 'turn/end' ? 'completed' : undefined
  if (notification) void window.desktop.notify({ sessionId: event.sessionId, kind: notification }).catch(() => {})
}
