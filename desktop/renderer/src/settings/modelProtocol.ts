export type ModelProtocol = 'responses' | 'anthropic_messages'
export const protocolNames: Record<ModelProtocol, string> = { responses: 'OpenAI Responses', anthropic_messages: 'Anthropic Messages' }
export const protocolURLs: Record<ModelProtocol, string> = { responses: 'https://api.openai.com/v1', anthropic_messages: 'https://api.anthropic.com/v1' }
export function modelProtocol(value?: ModelProtocol): ModelProtocol { return value ?? 'responses' }
export function changeProtocolURL(url: string, previous: ModelProtocol, next: ModelProtocol) {
  return !url || url === protocolURLs[previous] ? protocolURLs[next] : url
}
export function modelRequestURL(url: string, protocol: ModelProtocol) {
  return url.trim().replace(/\/+$/, '') + (protocol === 'anthropic_messages' ? '/messages' : '/responses')
}
