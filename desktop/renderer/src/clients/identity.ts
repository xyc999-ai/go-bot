export type ClientIdentity = { type: 'desktop'; platform: string; model: string }
export const clientIdentityHeader = 'X-Go-Bot-Client'
export const encodeClientIdentity = (identity: ClientIdentity) => encodeURIComponent(JSON.stringify(identity))
