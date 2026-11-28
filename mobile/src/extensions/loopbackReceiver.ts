import { requireOptionalNativeModule } from 'expo-modules-core'
import type { LoopbackReceiver } from '../../../frontend/src/extensions/useLoopbackAuthorization'
// A native build is required. Expo Go keeps the manual prompt when this native module is unavailable.
export const loopbackReceiver = requireOptionalNativeModule<LoopbackReceiver>('GoBotOAuthLoopback') ?? undefined
