export const permissionOptions = [
  { mode: 'danger-full-access', label: 'Full access', detail: 'Allow writes anywhere the Host tools can access.' },
  { mode: 'workspace-write', label: 'Workspace write', detail: 'Allow writes in this chat’s workspace. Ask before widening access.' },
  { mode: 'read-only', label: 'Read only', detail: 'Read files and ask before any writes.' },
] as const

export type PermissionMode = typeof permissionOptions[number]['mode']
export type SessionPermission = { mode: PermissionMode; approval: 'ask' | 'never' }

export function permissionLabel(mode: PermissionMode | undefined) {
  return permissionOptions.find(option => option.mode === mode)?.label
}
