import type { Context as CordisContext } from '@deepseek-ai/cordis'

export declare const apiVersion: '1'
export type JSONValue = null | boolean | number | string | JSONValue[] | { [key: string]: JSONValue }
export type JSONSchema = { [key: string]: JSONValue }
export declare class HostError extends Error {
  readonly code: string
  constructor(code: string, message: string)
}
export interface Registration { readonly id: string; dispose(): Promise<void> }
export interface ToolCall {
  readonly id: string
  readonly sessionId: string
  readonly cwd: string
  readonly workspace: string
  readonly signal: AbortSignal
}
export interface ToolResult { content: { type: 'text'; text: string }[]; data?: JSONValue }
export interface ToolDefinition {
  name: string; description: string; inputSchema: JSONSchema
  concurrency?: 'exclusive' | 'parallel'
  /** Omitted: no additional tool deadline; invocation cancellation still applies. */
  timeoutMs?: number
  execute(args: JSONValue, call: ToolCall): Promise<ToolResult>
}
export interface PreparationContext {
  readonly sessionId: string; readonly cwd: string; readonly workspace: string
  readonly signal: AbortSignal
  readonly model?: { readonly provider: string; readonly name: string }
}
export type ResourceBase = { kind: 'directory'; path: string } | { kind: 'url'; url: string; description?: string }
export interface Skill {
  name: string; description: string; whenToUse?: string
  modelInvocable?: boolean; userInvocable?: boolean; resourceBase?: ResourceBase
}
export interface SkillDefinition extends Skill { content: string }
export interface SkillSummary extends Skill { source: string; provider: string; path?: string }
export interface SkillContext { readonly cwd: string; readonly signal: AbortSignal }
export interface SkillProvider {
  id: string
  list(context: SkillContext): { items: (Skill & { key: JSONValue })[]; complete: boolean } | Promise<{ items: (Skill & { key: JSONValue })[]; complete: boolean }>
  get(key: JSONValue, context: SkillContext): SkillDefinition | null | Promise<SkillDefinition | null>
}
export type CredentialRecord = { kind: 'api-key'; key?: string; env?: Record<string, string> } | { kind: 'grant'; payload: JSONValue }
export interface CredentialDescription { configured: boolean; kind?: CredentialRecord['kind']; writable: boolean }
export interface CredentialSummary { key: string; kind: CredentialRecord['kind'] }
export interface AuthorizationMethod { id: string; label: string }
export interface AuthorizationNotice { message: string; url?: string; code?: string }
export type AuthorizationPrompt = { kind: 'text' | 'secret'; message: string; placeholder?: string; webInput?: 'json-file'; loopback?: { authorizationUrl: string; redirectUri: string; state: string } }
  | { kind: 'select'; message: string; options: { id: string; label: string; description?: string }[] }
export interface AuthorizationSession {
  readonly method: string; readonly signal: AbortSignal
  notify(notice: AuthorizationNotice): Promise<void>
  prompt(request: AuthorizationPrompt): Promise<string>
  /** One browser callback for this attempt. The provider library supplies state
   * and owns issuer/PKCE/token validation; receiving a callback is not a grant. */
  callback(options: { state: string }): Promise<{ readonly url: string; wait(): Promise<Record<string, string[]>> }>
  commit(record: CredentialRecord): Promise<void>
}
export interface AuthorizationFlow {
  key: string; title: string; methods: AuthorizationMethod[]
  run(session: AuthorizationSession): Promise<void>
  /** Native credential stores may confirm completion without commit(). */
  inspect?(context: {signal: AbortSignal}): Promise<{configured:boolean; accountLabel?:string}>
  disconnect?(context: {signal: AbortSignal}): Promise<void>
}
export interface AuthorizationSummary { key: string; title: string; methods: AuthorizationMethod[]; phase: string }
export interface AuthorizationAttempt { id: string; phase: string; authorizationUrl?: string; lastError?: string }
export interface McpOAuth {
  credentialKey?: string; clientId?: string; clientSecretRef?: string
  scopes?: string[]; scope?: string; requestTimeoutMs?: number; authorizationTimeoutMs?: number
}
export type McpConnection = ({ transport: 'stdio'; command: string; args?: string[]; env?: Record<string, string>; cwd?: string }
  | { transport: 'streamable-http'; url: string; headers?: Record<string, string> }) & { oauth?: McpOAuth }
export interface McpServiceView { name: string; state: string; toolCount: number; error?: string; authorization?: AuthorizationAttempt }
export interface QuestionRequest {
  questions: { id: string; question: string; header?: string; detail?: string; multiSelect?: boolean; options?: { label: string; description?: string }[] }[]
}
export interface QuestionAnswer { answers: { id: string; selected: string[]; custom?: string }[] }
export type ApprovalOutcome = 'allowed' | 'rejected' | 'cancelled' | 'unavailable'
export interface Host {
  /** Signal that this plugin has new external information. Contents remain in
   * the plugin's source and are read later; this never forces a turn now. */
  proactive: { changed(): Promise<void> }
  info(): Promise<{ userId: string; hostId: string; dataRoot: string; instanceId: string; apiVersion: '1' }>
  tools: { register(definition: ToolDefinition): Promise<Registration> }
  skills: {
    register(skill: SkillDefinition): Promise<Registration>
    registerDirectory(path: string): Promise<Registration>
    registerProvider(provider: SkillProvider): Promise<Registration>
    list(options?: { cwd?: string }): Promise<{ items: SkillSummary[]; complete: boolean }>
    get(name: string, options?: { cwd?: string }): Promise<(SkillSummary & SkillDefinition) | null>
    invalidate(): Promise<void>
  }
  prompts: { register(definition: { id: string; location: 'system' | 'context'; order?: number; text: string | ((context: PreparationContext) => string | Promise<string>) }): Promise<Registration> }
  credentials: {
    get(key: string): Promise<CredentialRecord | null>
    put(key: string, record: CredentialRecord): Promise<void>
    update(key: string, mutate: (current: CredentialRecord | null) => CredentialRecord | null | Promise<CredentialRecord | null>): Promise<CredentialRecord | null>
    remove(key: string): Promise<void>
    describe(key: string): Promise<CredentialDescription>
    list(): Promise<CredentialSummary[]>
    resolve(ref: string): Promise<string | null>
  }
  authorization: {
    register(flow: AuthorizationFlow): Promise<Registration>
    list(): Promise<AuthorizationSummary[]>
    cancel(key: string): Promise<void>
  }
  mcp: {
    register(definition: { name: string; connection: McpConnection; toolLoading?: 'eager' | 'on-demand' }): Promise<Registration>
    list(): Promise<McpServiceView[]>
    inspect(name: string): Promise<McpServiceView | null>
    restart(name: string): Promise<McpServiceView>
    authorize(name: string): Promise<AuthorizationAttempt>
    cancelAuthorization(name: string, attemptId: string): Promise<void>
    signOut(name: string): Promise<void>
  }
  approvals: { request(call: ToolCall, request: { reason: string; detail?: string }): Promise<ApprovalOutcome> }
  questions: { ask(call: ToolCall, request: QuestionRequest): Promise<QuestionAnswer> }
}
export interface HostEvents {
  'host/skills-changed': (event: { instanceId: string; source?: string }) => void
  'host/credential-changed': (event: { instanceId: string; key: string; configured: boolean }) => void
  'host/authorization-changed': (event: { instanceId: string; key: string; phase: string; error?: string }) => void
  'host/mcp-changed': (event: { instanceId: string; name: string; state: string; error?: string; toolCount: number }) => void
  'host/stopping': (event: { reason: string }) => void
}
declare module '@deepseek-ai/cordis' {
  interface Context { host: Host }
  interface Events extends HostEvents {}
}
export type Context = CordisContext
