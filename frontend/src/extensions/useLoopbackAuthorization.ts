import { useEffect, useRef, useState } from 'react'
import type { AuthorizationPrompt } from '../api/extensions'
import type { useAuthorization } from './useAuthorization'

export type Loopback = NonNullable<Extract<AuthorizationPrompt, { kind: 'text' | 'secret' }>['loopback']>
export interface LoopbackReceiver {
  open(id: string, value: Loopback): Promise<string>
  cancel(id: string): Promise<void>
}
export function validateLoopbackAnswer(value: string, config: Loopback) {
  const actual = new URL(value), expected = new URL(config.redirectUri)
  if (actual.origin !== expected.origin || actual.pathname !== expected.pathname || actual.username || actual.password || actual.hash ||
      actual.searchParams.getAll('state').length !== 1 || actual.searchParams.get('state') !== config.state) throw new Error('INVALID_CALLBACK')
  const codes = actual.searchParams.getAll('code'), errors = actual.searchParams.getAll('error')
  if (errors.length === 1 && errors[0] && !codes.length) throw new Error('ACCESS_DENIED')
  if (errors.length || codes.length !== 1 || !codes[0]) throw new Error('INVALID_CALLBACK')
  return actual.href
}
export function loopbackError(error: unknown) {
  const code = error instanceof Error ? error.message : ''
  if (code.includes('PORT_IN_USE')) return 'The authorization port is already in use. Close the other authorization and try again.'
  if (code.includes('TIMED_OUT')) return 'Authorization timed out. Start again to get a new login link.'
  if (code.includes('ACCESS_DENIED')) return 'Access was denied. You can start authorization again.'
  if (code.includes('CANCELLED')) return 'Authorization was cancelled. You can start again.'
  if (code.includes('SUBMIT_FAILED')) return 'Could not submit the callback. Start authorization again.'
  return 'Could not receive the authorization callback. Start authorization again.'
}

// A panel owns its local receiver. The Host still owns admission and completion.
export function useLoopbackAuthorization(state: ReturnType<typeof useAuthorization>, receiver?: LoopbackReceiver) {
  const attempt = state.attempt
  const prompt = attempt?.status === 'running' ? attempt.prompts.find(p => p.kind === 'text' && p.loopback) : undefined
  const config = prompt?.kind === 'text' ? prompt.loopback : undefined
  const id = attempt && prompt ? attempt.id + ':' + prompt.id : undefined
  const remembered = useRef<{ attempt: string; url: string } | undefined>(undefined)
  if (receiver && config && attempt) remembered.current = { attempt: attempt.id, url: config.authorizationUrl }
  const authorizationUrl = remembered.current?.attempt === attempt?.id ? remembered.current?.url : undefined
  const [phase, setPhase] = useState<'idle' | 'waiting' | 'submitting' | 'failed'>('idle'), [error, setError] = useState('')
  const current = useRef(state); current.current = state
  const generation = useRef(0), inFlight = useRef(false)
  useEffect(() => {
    generation.current++; inFlight.current = false; setPhase('idle')
    return () => { generation.current++; if (receiver && id) void receiver.cancel(id).catch(() => {}) }
  }, [id, receiver])
  useEffect(() => { setError('') }, [attempt?.id])
  async function begin() {
    if (!receiver || !config || !id || !prompt || inFlight.current || current.current.busy) return
    inFlight.current = true
    const run = ++generation.current
    setError(''); setPhase('waiting')
    try {
      const value = validateLoopbackAnswer(await receiver.open(id, config), config)
      if (run !== generation.current) return
      setPhase('submitting')
      // Answer is ephemeral; it is never added to chat logs or client storage.
      if (!await current.current.answer(prompt.id, value)) throw new Error('SUBMIT_FAILED')
      if (run === generation.current) { setPhase('idle'); current.current.reload() }
    } catch (error) {
      if (run !== generation.current) return
      setError(loopbackError(error)); setPhase('failed')
      // Release the CLI and its port before offering a fresh attempt.
      await current.current.cancel()
    } finally { if (run === generation.current) inFlight.current = false }
  }
  return { prompt: receiver ? prompt : undefined, authorizationUrl, phase, error, begin }
}
