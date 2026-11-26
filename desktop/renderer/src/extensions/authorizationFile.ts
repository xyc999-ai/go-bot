// Configuration is an authorization answer, never a chat attachment or upload.
export async function readAuthorizationJSON(file: { size: number; text(): Promise<string> }): Promise<string> {
  if (file.size > 16384) throw new Error('The configuration file must be 16 KB or smaller.')
  let value: unknown
  try { value = JSON.parse((await file.text()).replace(/^\uFEFF/, '')) }
  catch { throw new Error('Select a valid JSON configuration file.') }
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Select a valid JSON configuration file.')
  const answer = JSON.stringify(value)
  if (new TextEncoder().encode(answer).byteLength > 16384) throw new Error('The configuration file must be 16 KB or smaller.')
  return answer
}
