// One signed-in client keeps its request alive when the plugin screen closes.
// Reads/navigation stay independent; only package mutations are serialized.
export type OperationState = { busy: string; error: string; notice: string; revision: number }
export class ExtensionOperations {
  private state: OperationState = { busy: '', error: '', notice: '', revision: 0 }
  private listeners = new Set<() => void>()
  snapshot = () => this.state
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener) } }
  private update(change: Partial<OperationState>) { this.state = { ...this.state, ...change }; this.listeners.forEach(listener => listener()) }
  setError = (error: string) => this.update({ error })
  setNotice = (notice: string) => this.update({ notice })
  async run(label: string, work: () => Promise<unknown>, failure: (error: unknown) => string, success: string) {
    if (this.state.busy) return false
    this.update({ busy: label, error: '', notice: '' })
    try { await work(); this.update({ notice: success }); return true }
    catch (error) { this.update({ error: failure(error) }); return false }
    finally { this.update({ busy: '', revision: this.state.revision + 1 }) }
  }
}
const clients = new WeakMap<object, ExtensionOperations>()
export function extensionOperations(client: object) {
  let state = clients.get(client)
  if (!state) { state = new ExtensionOperations(); clients.set(client, state) }
  return state
}
