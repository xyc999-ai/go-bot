import { Platform } from 'react-native'
import * as Device from 'expo-device'
import type { ClientIdentity } from '../../frontend/src/clients/identity'

export async function mobileClientIdentity(): Promise<ClientIdentity> {
 const fallback = Platform.OS === 'ios' ? Platform.isPad ? 'iPad' : 'iPhone' : 'Android'
 const model = Array.from(Device.modelName?.trim() || fallback).slice(0, 120).join('')
 return { type: 'mobile', platform: Platform.OS, model }
}
