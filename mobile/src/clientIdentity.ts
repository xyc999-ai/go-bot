import { Platform } from 'react-native'
import { File, Paths } from 'expo-file-system'
import { newClientId, validClientId, type ClientIdentity } from '../../frontend/src/clients/identity'
let identity: Promise<ClientIdentity> | undefined
export function mobileClientIdentity(): Promise<ClientIdentity> {
 return identity ??= (async () => {
  const file = new File(Paths.document, 'client-identity.txt')
  let id: string | undefined
  try { if (file.exists) { const saved = await file.text(); if (validClientId(saved)) id = saved } } catch {}
  if (!id) {
   id = newClientId()
   try { if (!file.exists) file.create(); file.write(id) } catch {}
  }
  const model = Platform.OS === 'android' ? Platform.constants.Model : Platform.OS === 'ios' && Platform.isPad ? 'iPad' : undefined
  return { id, type: 'mobile', platform: Platform.OS, ...(model ? { model: model.slice(0, 120) } : {}) }
 })()
}
