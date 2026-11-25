export type ClientIdentity = { id: string; type: 'web' | 'mobile' | 'desktop'; platform?: string; model?: string }
export type ClientDevice = ClientIdentity & { status: 'connected' | 'disconnected'; lastSeenAt: number }
export const clientIdentityHeader = 'X-Go-Bot-Client'
export const validClientId = (id: unknown): id is string => typeof id === 'string' && /^[A-Za-z0-9_-]{8,80}$/.test(id)
// An anonymous installation identifier, not an authentication credential.
export const newClientId = () => 'client-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 12) + Math.random().toString(36).slice(2, 12)
export const encodeClientIdentity = (identity: ClientIdentity) => encodeURIComponent(JSON.stringify(identity))
let browserId: string | undefined
export function webClientIdentity(): ClientIdentity {
 if (!browserId) {
  try {
   const saved = localStorage.getItem('go-bot-client-id')
   if (validClientId(saved)) browserId = saved
   else { browserId = newClientId(); localStorage.setItem('go-bot-client-id', browserId) }
  } catch { browserId ??= newClientId() }
 }
 return { id: browserId, type: 'web' }
}
export function clientDisplayName(device: ClientIdentity) {
 if (device.type === 'web') return 'Web'
 if (device.type === 'mobile' && /^(sdk_|google_sdk|Android SDK built for|emulator)/i.test(device.model ?? '')) return 'Android Emulator'
 if (device.model) return device.model
 if (device.platform === 'ios') return 'iPhone'
 if (device.platform === 'android') return 'Android'
 return device.type === 'desktop' ? 'Desktop' : 'Mobile'
}

export function clientLastSeen(time: number, language: 'zh-CN' | 'en') {
 const date = new Date(time)
 if (!Number.isFinite(time) || time <= 0 || Number.isNaN(date.getTime())) return ''
 const clock = String(date.getHours()).padStart(2, '0') + ':' + String(date.getMinutes()).padStart(2, '0')
 if (language === 'zh-CN') return `${date.getMonth() + 1}月${date.getDate()}日 ${clock}`
 const month = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'][date.getMonth()]
 return `${month} ${date.getDate()}, ${clock}`
}
