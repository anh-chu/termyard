import type { Session } from '../hooks/useSessions'
import type { ToolEvent } from '../hooks/useToolEvents'
import type { ActivitySnapshot } from '../hooks/useActivity'

export type SessionState = 'needs_you' | 'working' | 'idle' | 'offline'

export interface SessionSignal {
  state: SessionState
  loud: boolean
  reason?: string
  tool?: string
  agentCount: number
}

const agentCommands = new Set(['claude', 'codex', 'copilot', 'opencode'])
const shellCommands = new Set(['bash', 'zsh', 'fish', 'sh', 'dash', 'ksh', 'csh', 'tcsh', 'tmux', 'login'])
const loudStatuses = new Set(['waiting', 'stuck', 'error'])

export const stateRank: Record<SessionState, number> = {
  needs_you: 0,
  working: 1,
  idle: 2,
  offline: 3,
}

export function isSessionActive(session: Session): boolean {
  if (!session.windows) return false
  return session.windows.some(w =>
    w.panes?.some(p => p.current_command && !shellCommands.has(p.current_command)),
  )
}

export function sessionSignal(
  session: Session,
  events: ToolEvent[],
  activity: ActivitySnapshot | undefined,
  inActiveTurn: boolean,
): SessionSignal {
  const eventPanes = new Set(events.map(e => e.pane).filter((pane): pane is string => !!pane))
  const agentCount = (session.windows || []).reduce(
    (n, w) => n + (w.panes || []).filter(p => agentCommands.has(p.current_command) || eventPanes.has(p.id)).length,
    0,
  )
  const loudEvent = events.find(e => loudStatuses.has(e.status))
  const tool = (loudEvent || events[0])?.tool

  if (loudEvent) {
    return { state: 'needs_you', loud: true, reason: loudEvent.status, tool, agentCount }
  }

  if (session.host && session.host_online === false) {
    return { state: 'offline', loud: false, tool, agentCount }
  }

  const working = inActiveTurn || isSessionActive(session) || (activity !== undefined && activity.idle_seconds <= 5)
  if (working) {
    return { state: 'working', loud: false, tool, agentCount }
  }

  return { state: 'idle', loud: false, tool, agentCount }
}
