import { useEffect, useMemo, useState } from 'react'
import { Session, sessionKey } from '../hooks/useSessions'
import { Host } from '../hooks/useHosts'
import { ToolEvent } from '../hooks/useToolEvents'
import { ActivitySnapshot } from '../hooks/useActivity'
import { usePreferences } from '../hooks/usePreferences'
import { toolColors, statusConfig, signalTreatment } from '../theme'
import { stateRank, sessionSignal, isSessionActive } from '../lib/sessionState'

interface OverviewProps {
  sessions: Session[]
  hosts: Host[]
  hiddenSet: Set<string>
  backgroundSet: Set<string>
  onSessionSelect: (session: Session) => void
  getSessionEvents: (session: string) => ToolEvent[]
  getSessionActivity: (session: string) => ActivitySnapshot | undefined
  isSessionInActiveTurn: (key: string) => boolean
  onJumpToSession: (session: string, windowIndex?: number, pane?: string) => void
  onDismissAlert: (evt: ToolEvent) => void
}

interface SystemStats {
  os: string
  arch: string
  cpus: number
  goroutines: number
  termyard_mem_mb: number
  load?: { '1m': number; '5m': number; '15m': number }
  uptime_seconds?: number
  memory?: { total_mb: number; used_mb: number; available_mb: number; percent: number }
  cpu_percent?: number
  processes?: { name: string; count: number }[]
}

interface Stats {
  sessions: { total: number; attached: number; detached: number }
  windows: number
  panes: number
  agent_panes: number
  agents: { active: number; waiting: number; stuck: number; error: number }
  processes: { name: string; count: number }[]
  system?: SystemStats
}

const agentCommands = new Set(['claude', 'codex', 'copilot', 'opencode'])

function Sparkline({ data, height = 20 }: { data: number[]; height?: number }) {
  if (!data || data.length === 0) return null
  const max = Math.max(...data, 1)
  const viewWidth = data.length
  const barWidth = 1
  return (
    <svg viewBox={`0 0 ${viewWidth} ${height}`} preserveAspectRatio="none" width="100%" height={height} className="block">
      {data.map((val, i) => {
        const barHeight = (val / max) * height
        return (
          <rect
            key={i}
            x={i * barWidth}
            y={height - barHeight}
            width={Math.max(barWidth - 0.05, 0.05)}
            height={barHeight}
            style={{ fill: val > 0 ? 'var(--chart-primary)' : 'var(--muted)' }}
            opacity={val > 0 ? 0.7 : 0.3}
          />
        )
      })}
    </svg>
  )
}

function formatUptime(created: string, format: string = 'relative'): string {
  if (format === 'absolute') return new Date(created).toLocaleTimeString()
  const diff = Date.now() - new Date(created).getTime()
  const hours = Math.floor(diff / 3600000)
  if (hours < 1) return `${Math.floor(diff / 60000)}m`
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

function formatSystemUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const mins = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${mins}m`
  return `${mins}m`
}

function ProcessBar({ processes, totalPanes }: { processes: { name: string; count: number }[]; totalPanes: number }) {
  if (processes.length === 0) return null
  const max = processes[0]?.count || 1
  return (
    <div className="flex flex-col gap-2">
      {processes.slice(0, 10).map(p => {
        const isAgent = agentCommands.has(p.name)
        return (
          <div key={p.name} className="flex items-center gap-3">
            <span className="w-[92px] text-xs font-semibold text-right overflow-hidden text-ellipsis whitespace-nowrap shrink-0" style={{ color: isAgent ? 'var(--ink)' : 'var(--mute)' }}>
              {p.name}
            </span>
            <div className="flex-1 h-1.5 bg-surface-elevated rounded-full overflow-hidden">
              <div
                className="h-full rounded-full min-w-[2px]"
                style={{
                  width: `${(p.count / max) * 100}%`,
                  background: isAgent ? (toolColors[p.name] || 'var(--chart-secondary)') : 'var(--border)',
                }}
              />
            </div>
            <span className="text-xs font-bold text-mute/50 w-[48px] shrink-0 text-right">{p.count}</span>
          </div>
        )
      })}
      <div className="text-[10px] text-mute/50">{totalPanes} panes total</div>
    </div>
  )
}

function SystemStatsCard({ system }: { system: SystemStats }) {
  return (
    <div className="bg-surface border border-hairline rounded-lg p-5 flex flex-col gap-4">
      {system.cpu_percent !== undefined && <div title={`CPU ${system.cpu_percent}%`}><div className="h-1.5 rounded-full bg-surface-elevated overflow-hidden"><div className="h-full rounded-full" style={{ width: `${Math.min(system.cpu_percent, 100)}%`, background: system.cpu_percent > 90 ? 'var(--destructive)' : system.cpu_percent > 70 ? 'var(--warning)' : 'var(--chart-primary)' }} /></div></div>}
      {system.memory && <div title={`MEM ${system.memory.percent}%`}><div className="h-1.5 rounded-full bg-surface-elevated overflow-hidden"><div className="h-full rounded-full" style={{ width: `${Math.min(system.memory.percent, 100)}%`, background: system.memory.percent > 90 ? 'var(--destructive)' : system.memory.percent > 70 ? 'var(--warning)' : 'var(--chart-secondary)' }} /></div></div>}
      <div className="grid grid-cols-2 gap-4 mt-2">
        {system.load && <div><div className="text-xs font-bold text-mute/50 mb-1.5">Load average</div><div className="text-[14px] text-ink font-mono font-medium">{system.load['1m'].toFixed(2)} <span className="text-mute">{system.load['5m'].toFixed(2)}</span> <span className="text-mute/60">{system.load['15m'].toFixed(2)}</span></div></div>}
        {system.memory && <div><div className="text-xs font-bold text-mute/50 mb-1.5">Memory</div><div className="text-[14px] text-ink font-medium">{(system.memory.used_mb / 1024).toFixed(1)}<span className="text-mute/60 font-normal"> / {(system.memory.total_mb / 1024).toFixed(1)} GB</span></div></div>}
        {system.uptime_seconds !== undefined && <div><div className="text-xs font-bold text-mute/50 mb-1.5">System uptime</div><div className="text-[14px] text-ink font-medium">{formatSystemUptime(system.uptime_seconds)}</div></div>}
        <div><div className="text-xs font-bold text-mute/50 mb-1.5">CPUs</div><div className="text-[14px] text-ink font-medium">{system.cpus} <span className="text-mute/60 font-normal">{system.arch}</span></div></div>
      </div>
    </div>
  )
}

function HostStatsSection({ host, totalPanes }: { host: Host; totalPanes: number }) {
  const hostStats = host.stats as SystemStats | undefined
  if (!hostStats) return null
  const processes = hostStats.processes || []
  return (
    <div className="mb-8">
      <h3 className="font-display text-[13px] font-bold text-ink mb-4 flex items-center gap-2">
        <span className={`w-1.5 h-1.5 rounded-full ${host.online ? 'bg-success' : 'bg-stone'}`} />
        {host.name}
      </h3>
      <div className="grid grid-cols-[repeat(auto-fit,minmax(320px,1fr))] gap-4">
        {processes.length > 0 && <div><div className="text-xs font-bold text-mute/50 mb-2.5 ml-1">Processes</div><div className="bg-surface border border-hairline rounded-lg p-5"><ProcessBar processes={processes} totalPanes={totalPanes} /></div></div>}
        <div><div className="text-xs font-bold text-mute/50 mb-2.5 ml-1">System</div><SystemStatsCard system={hostStats} /></div>
      </div>
    </div>
  )
}

export function Overview({ sessions, hosts, hiddenSet, backgroundSet, onSessionSelect, getSessionEvents, getSessionActivity, isSessionInActiveTurn, onJumpToSession, onDismissAlert }: OverviewProps) {
  const [stats, setStats] = useState<Stats | null>(null)
  const { prefs } = usePreferences()
  const hasMultipleHosts = hosts.length > 1

  const foregroundSessions = useMemo(() => sessions.filter(s => !hiddenSet.has(sessionKey(s)) && !backgroundSet.has(sessionKey(s))), [sessions, hiddenSet, backgroundSet])
  const hiddenCount = sessions.length - foregroundSessions.length

  useEffect(() => {
    const fetchStats = async () => {
      try {
        const res = await fetch('/api/stats')
        if (res.ok) setStats(await res.json())
      } catch {}
    }
    fetchStats()
    const ms = (prefs.overview_refresh_interval || 5) * 1000
    const interval = setInterval(fetchStats, ms)
    return () => clearInterval(interval)
  }, [prefs.overview_refresh_interval])

  const grouped = useMemo(() => {
    const groups = new Map<string, Array<{ session: Session; key: string; signal: ReturnType<typeof sessionSignal>; event: ToolEvent | undefined; events: ToolEvent[]; activity: ActivitySnapshot | undefined }>>()
    for (const session of foregroundSessions) {
      const key = sessionKey(session)
      const events = getSessionEvents(key)
      const activity = getSessionActivity(key)
      const signal = sessionSignal(session, events, activity, isSessionInActiveTurn(key))
      const groupLabel = hasMultipleHosts ? (session.host_name || 'Local') : 'Sessions'
      if (!groups.has(groupLabel)) groups.set(groupLabel, [])
      groups.get(groupLabel)!.push({ session, key, signal, event: events.find(e => e.status === 'waiting' || e.status === 'stuck' || e.status === 'error'), events, activity })
    }

    return Array.from(groups.entries())
      .sort(([a], [b]) => (a === 'Local' || a === 'Sessions' ? -1 : b === 'Local' || b === 'Sessions' ? 1 : a.localeCompare(b)))
      .map(([groupLabel, items]) => ({
        groupLabel,
        items: items.sort((a, b) => stateRank[a.signal.state] - stateRank[b.signal.state] || a.session.name.localeCompare(b.session.name)),
      }))
  }, [foregroundSessions, getSessionEvents, getSessionActivity, hasMultipleHosts, isSessionInActiveTurn])

  const activeHostSections = hosts.filter(h => h.stats)

  return (
    <div className="flex-1 p-8 overflow-y-auto font-sans text-sm font-medium bg-canvas">
      {foregroundSessions.length === 0 && (
        <div className="mb-10">
          <div className="text-mute text-[13px] font-medium mb-4 ml-1">
            {sessions.length === 0 ? 'No tmux sessions found. Start a tmux session to get started.' : 'All sessions are hidden or backgrounded.'}
            {hiddenCount > 0 && <span className="text-mute/50"> {' '}({hiddenCount} hidden/backgrounded)</span>}
          </div>
          <div className="grid grid-cols-3 gap-2 max-w-3xl">
            {Array.from({ length: 6 }).map((_, i) => <div key={i} className="tex-yardgrid border border-hairline/40 rounded-sm aspect-[4/3] opacity-50" />)}
          </div>
        </div>
      )}

      {grouped.map(({ groupLabel, items }) => (
        <div key={groupLabel} className="mb-10">
          <h3 className="font-display text-[13px] font-bold text-ink mb-4 flex items-center gap-2">
            {groupLabel}
            {hasMultipleHosts && <span className={`text-[10px] font-medium ${items[0]?.session.host_online !== false ? 'text-success' : 'text-mute/50'}`}>{items[0]?.session.host_online !== false ? 'online' : 'offline'}</span>}
            <span className="text-mute/40 font-bold text-xs ml-1">({items.length})</span>
          </h3>
          <div className="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-2">
            {items.map(({ session, key, signal, event, activity }) => {
              const t = signalTreatment[signal.state]
              const isWaiting = signal.state === 'needs_you'
              const loudEvent = event || getSessionEvents(key).find(e => e.status === 'waiting' || e.status === 'stuck' || e.status === 'error')
              return (
                <button
                  key={key}
                  onClick={() => {
                    if (signal.state === 'needs_you' && loudEvent) onJumpToSession(loudEvent.host ? `${loudEvent.host}/${loudEvent.session}` : loudEvent.session, loudEvent.window, loudEvent.pane)
                    else onSessionSelect(session)
                  }}
                  className={`text-left border bg-surface rounded-sm p-4 min-h-36 flex flex-col gap-2 transition-all ${signal.state === 'offline' ? 'opacity-60' : ''}`}
                  style={{
                    borderColor: isWaiting ? 'var(--warning)' : 'var(--hairline)',
                    boxShadow: isWaiting ? '0 0 0 1px var(--warning), 0 0 12px color-mix(in oklch, var(--warning) 25%, transparent)' : undefined,
                  }}
                >
                  <div className="flex items-start gap-2">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-start gap-2">
                        <span className={`font-display text-[15px] font-bold truncate ${signal.state === 'idle' ? 'text-mute' : 'text-ink'}`}>{session.display_name || session.name}</span>
                        {session.attached && <span className="text-[9px] font-bold tracking-wider text-success px-1.5 py-[1.5px] rounded-sm bg-success/10 border border-success/20">attached</span>}
                      </div>
                      <div className="text-[10px] text-mute/60">{formatUptime(session.created, prefs.timestamp_format)}</div>
                    </div>
                    {signal.state === 'needs_you' && loudEvent && (
                      <button
                        onClick={(e) => { e.stopPropagation(); onDismissAlert(loudEvent) }}
                        className="text-mute/30 hover:text-ink transition-colors leading-none"
                        title="Dismiss alert"
                      >
                        ×
                      </button>
                    )}
                  </div>

                  <div className="flex items-center gap-3 text-xs font-bold text-mute/70">
                    <span className="flex items-center gap-1.5"><svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="18" height="18" rx="2" ry="2"/></svg>{session.windows?.length || 0}</span>
                    {signal.agentCount > 0 && <span className="flex items-center gap-1.5" style={{ color: toolColors.claude }}><svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" /></svg>{signal.agentCount}</span>}
                    {signal.state === 'working' ? <span className="text-success">working</span> : signal.state === 'idle' ? <span className="text-mute/40">idle</span> : signal.state === 'offline' ? <span className="text-mute/40">offline</span> : signal.state === 'needs_you' ? <span className="text-warning font-bold">needs you</span> : null}
                  </div>

                  {signal.state === 'needs_you' && loudEvent ? (
                    <div className="text-[12px] text-ink leading-snug flex-1">
                      <span style={{ color: toolColors[loudEvent.tool] || 'var(--mute)' }} className="font-bold">{loudEvent.tool.toUpperCase()}</span>{' '}
                      <span style={{ color: t.text }} className="font-medium">{(statusConfig[loudEvent.status] || { label: loudEvent.status }).label}</span>{' '}
                      <span className="text-mute/70">{loudEvent.host_name ? `${loudEvent.host_name}: ` : ''}{loudEvent.message || 'Needs attention'}</span>
                    </div>
                  ) : signal.state === 'working' ? (
                    prefs.sparklines_visible && activity?.sparkline ? (
                      <div className="mt-auto pt-2 border-t border-hairline/40">
                        <Sparkline data={activity.sparkline} height={18} />
                      </div>
                    ) : null
                  ) : (
                    <div className="mt-auto text-[12px] text-mute/60">{isSessionActive(session) ? 'active' : 'calm'}</div>
                  )}
                </button>
              )
            })}
          </div>
        </div>
      ))}

      <details open className="mt-4 rounded-md border border-hairline bg-surface tex-yardgrid overflow-hidden">
        <summary className="cursor-pointer list-none px-4 py-3 flex items-center justify-between font-display text-[13px] text-ink">
          <span>Yard health</span>
          <span className="text-mute/60 text-xs">System stats</span>
        </summary>
        <div className="px-4 pb-4 pt-1">
          {hasMultipleHosts ? (
            activeHostSections.map(host => {
              const paneCount = (host.sessions || []).reduce((n, s: any) => n + (s.windows || []).reduce((wn: number, w: any) => wn + (w.panes || []).length, 0), 0)
              return <HostStatsSection key={host.id} host={host} totalPanes={paneCount} />
            })
          ) : (
            <div className="grid grid-cols-[repeat(auto-fit,minmax(320px,1fr))] gap-4">
              {stats?.processes && stats.processes.length > 0 && <div><h3 className="font-display text-[13px] font-bold text-ink mb-4 ml-1">Processes</h3><div className="bg-surface border border-hairline rounded-lg p-5"><ProcessBar processes={stats.processes} totalPanes={stats.panes} /></div></div>}
              {stats?.system && <div><h3 className="font-display text-[13px] font-bold text-ink mb-4 ml-1">System</h3><SystemStatsCard system={stats.system} /></div>}
            </div>
          )}
        </div>
      </details>
    </div>
  )
}
