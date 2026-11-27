export type ClientIdentity = { type: 'mobile' | 'desktop'; platform: string; model: string }
export type ClientDevice = { id: string; type: 'web' | 'mobile' | 'desktop'; platform?: string; model?: string; status: 'connected' | 'disconnected'; lastSeenAt: number }
export const clientIdentityHeader = 'X-Go-Bot-Client'
export const encodeClientIdentity = (identity: ClientIdentity) => encodeURIComponent(JSON.stringify(identity))
// Match the account's name-based device groups, not an installation or connection ID.
export const clientIdentityKey = (device: Pick<ClientDevice, 'type' | 'platform' | 'model'>) => JSON.stringify([device.type, device.platform ?? '', device.model?.trim() ?? ''])
export function clientDisplayName(device: Pick<ClientDevice, 'type' | 'platform' | 'model'>) {
 if (device.type === 'web') return 'Web'
 if (device.type === 'mobile' && /^(sdk_|google_sdk|Android SDK built for|emulator)/i.test(device.model ?? '')) return 'Android Emulator'
 if (device.model) return device.model.replace(/^Go Bot\s*[·∙]\s*/, '')
 if (device.platform === 'ios') return 'iPhone'
 if (device.platform === 'android') return 'Android'
 return device.type === 'desktop' ? 'Desktop' : 'Mobile'
}

export function clientLastSeen(time: number, language: string) {
 const date = new Date(time)
 if (!Number.isFinite(time) || time <= 0 || Number.isNaN(date.getTime())) return ''
 const clock = String(date.getHours()).padStart(2, '0') + ':' + String(date.getMinutes()).padStart(2, '0')
 if (language === 'zh-CN') return `${date.getMonth() + 1}月${date.getDate()}日 ${clock}`
 if (language !== 'en') return new Intl.DateTimeFormat(language, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }).format(date)
 const month = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'][date.getMonth()]
 return `${month} ${date.getDate()}, ${clock}`
}
