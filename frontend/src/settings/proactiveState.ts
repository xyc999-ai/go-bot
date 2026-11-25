export type ProactiveStatus = {
  enabled: boolean
  phase: string
  nextCheckAt?: number
  nextCheckReason?: string
  lastRunStartedAt?: number
  lastCompletedAt?: number
  error?: string
}

export const proactiveChinese: Record<string, string> = {
  'Proactive assistance': '主动服务',
  'Enable proactive assistance': '启用主动服务',
  'Keeps up with your recent conversations and memory, investigates useful opportunities, and shares relevant findings in your main chat.': '结合近期交流和长期记忆，自主研究值得关注的事情，并在合适时将发现带到主会话。',
  'Uses the main chat’s model and connected services while your Host is running. Model and service usage may incur charges.': 'Host 运行时使用主会话的模型及已连接的服务，可能产生相应的模型和服务用量费用。',
  'The agent chooses when to check again. New information can bring the next check forward.': '助手自主决定下次检查时间；有新信息时，也可能提前检查。',
  'Loading proactive settings…': '正在读取主动服务设置…',
  'Could not load proactive settings.': '无法读取主动服务设置。',
  'Could not save proactive settings.': '无法保存主动服务设置。',
  'Settings were saved, but the Host could not apply them. Retry or reopen the account.': '设置已保存，但 Host 未能应用。请重试或重新打开账号。',
  'Refresh status': '刷新状态',
  'Retry applying': '重新应用',
  'Checking for useful updates': '正在查看值得关注的新信息',
  'Waiting for the next check': '等待下次检查',
  'Waiting for a model': '等待可用模型',
  'Waiting for the main chat': '等待主会话就绪',
  'Host is stopped': 'Host 已停止',
  'Proactive assistance is off': '主动服务已关闭',
  'The last check could not finish': '上次检查未能完成',
  'Host status is unavailable': '暂时无法读取 Host 状态',
  'Next check': '下次检查',
  'Last completed': '最近完成',
  'Waiting for the first check': '等待首次检查',
  'No checks while turned off': '关闭期间不执行检查',
  'Add a default model to start proactive assistance.': '配置默认模型后即可开始主动服务。',
  'Choose an available model for the main chat to resume proactive assistance.': '请为主会话选择可用模型，以继续主动服务。',
  'The last check could not finish. It can resume after configuration or information changes.': '上次检查未能完成。配置恢复或有新信息后，可以继续运行。',
  'Open model settings': '打开模型设置',
  'Set up a model before enabling proactive assistance.': '请先配置模型，再开启主动服务。',
  'The main chat’s selected model is unavailable. Choose an available model before enabling proactive assistance.': '主会话选择的模型已不可用，请先选择可用模型，再开启主动服务。',
}

export function proactiveEnableProblem(code: string) {
  if (code === 'model_not_configured') return 'Set up a model before enabling proactive assistance.'
  if (code === 'model_selection_unavailable') return 'The main chat’s selected model is unavailable. Choose an available model before enabling proactive assistance.'
  return ''
}

export function proactivePhase(status: ProactiveStatus) {
  if (!status.enabled) return 'Proactive assistance is off'
  return ({ running: 'Checking for useful updates', 'waiting-for-model': 'Waiting for a model', 'waiting-for-main': 'Waiting for the main chat', 'host-stopped': 'Host is stopped', unavailable: 'Host status is unavailable', error: 'The last check could not finish' } as Record<string, string>)[status.phase] ?? 'Waiting for the next check'
}

export function proactiveProblem(status: ProactiveStatus) {
  if (!status.error || !status.enabled) return ''
  if (status.error === 'model_not_configured') return 'Add a default model to start proactive assistance.'
  if (status.error === 'model_selection_unavailable') return 'Choose an available model for the main chat to resume proactive assistance.'
  if (status.error === 'host_state_unavailable') return 'Host status is unavailable'
  return 'The last check could not finish. It can resume after configuration or information changes.'
}
