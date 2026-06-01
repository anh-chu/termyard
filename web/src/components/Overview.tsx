import { useState, useEffect } from 'react'
import { Session, sessionKey } from '../hooks/useSessions'
import { Host } from '../hooks/useHosts'
import { ToolEvent } from '../hooks/useToolEvents'
import { ActivitySnapshot } from '../hooks/useActivity'
import { usePreferences } from '../hooks/usePreferences'
import { toolColors, statusConfig } from '../theme'

interface OverviewProps {
  sessions: Session[]
  hosts: Host[]
  onSessionSelect: (session: Session) => void
  getSessionEvents: (session: string) => ToolEvent[]
  getSessionActivity: (session: string) => ActivitySnapshot | undefined
  pendingAlerts: ToolEvent[]
  onJumpToSession: (session: string, windowIndex?: number, pane?: string) => void
  onDismissAlert: (evt: ToolEvent) => void
}

interface SystemStats {
  os: string
  arch: string
  cpus: number
  goroutines: number
  guppi_mem_mb: number
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
const shellCommands = new Set(['bash', 'zsh', 'fish', 'sh', 'dash', 'ksh', 'csh', 'tcsh', 'tmux', 'login'])

function isSessionActive(session: Session): boolean {
  if (!session.windows) return false
  return session.windows.some(w =>
    w.panes?.some(p => p.current_command && !shellCommands.has(p.current_command))
  )
}

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
  if (format === 'absolute') {
    return new Date(created).toLocaleTimeString()
  }
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

function UsageBar({ percent, color, label }: { percent: number; color: string; label: string }) {
  return (
    <div className="flex items-center gap-3">
      <span className="w-10 text-xs text-mute font-semibold text-right shrink-0">{label}</span>
      <div className="flex-1 h-1.5 bg-surface-elevated rounded-full overflow-hidden">
        <div
          className="h-full rounded-full transition-[width] duration-300"
          style={{
            width: `${Math.min(percent, 100)}%`,
            background: percent > 90 ? 'var(--destructive)' : percent > 70 ? 'var(--warning)' : color,
          }}
        />
      </div>
      <span className="w-10 text-xs text-mute font-bold shrink-0">{Math.round(percent)}%</span>
    </div>
  )
}

function StatCard({ label, value, sub, color }: { label: string; value: string | number; sub?: string; color?: string }) {
  return (
    <div className="bg-surface border border-hairline rounded-lg p-5 flex-1 min-w-[140px] transition-all hover:border-hairline/60">
      <div className="text-3xl font-bold tracking-tight" style={{ color: color || 'var(--foreground)' }}>{value}</div>
      <div className="text-xs font-bold text-mute uppercase tracking-widest mt-1.5">{label}</div>
      {sub && <div className="text-xs font-medium text-mute/50 mt-1">{sub}</div>}
    </div>
  )
}

function ProcessBar({ processes, totalPanes }: { processes: { name: string; count: number }[]; totalPanes: number }) {
  if (processes.length === 0) return null
  const max = processes[0]?.count || 1

  return (
    <div className="flex flex-col gap-2">
      {processes.slice(0, 10).map(p => {
        const isAgent = agentCommands.has(p.name)
        const pct = totalPanes > 0 ? (p.count / totalPanes) * 100 : 0
        return (
          <div key={p.name} className="flex items-center gap-3">
            <span
              className="w-[100px] text-xs font-semibold text-right overflow-hidden text-ellipsis whitespace-nowrap shrink-0"
              style={{
                color: isAgent ? (toolColors[p.name] || 'var(--chart-secondary)') : 'var(--muted-foreground)',
              }}
            >
              {p.name}
            </span>
            <div className="flex-1 h-1.5 bg-surface-elevated rounded-full overflow-hidden">
              <div
                className="h-full rounded-full min-w-[2px] transition-all"
                style={{
                  width: `${(p.count / max) * 100}%`,
                  background: isAgent ? (toolColors[p.name] || 'var(--chart-secondary)') : 'var(--border)',
                }}
              />
            </div>
            <span className="text-xs font-bold text-mute/50 w-[50px] shrink-0">
              {p.count}
            </span>
          </div>
        )
      })}
    </div>
  )
}

function SystemStatsCard({ system }: { system: SystemStats }) {
  return (
    <div className="bg-surface border border-hairline rounded-lg p-5 flex flex-col gap-4">
      {system.cpu_percent !== undefined && (
        <UsageBar percent={system.cpu_percent} color="var(--chart-primary)" label="CPU" />
      )}
      {system.memory && (
        <UsageBar percent={system.memory.percent} color="var(--chart-secondary)" label="MEM" />
      )}
      <div className="grid grid-cols-2 gap-4 mt-2">
        {system.load && (
          <div>
            <div className="text-xs font-bold text-mute/50 uppercase tracking-widest mb-1.5">Load Average</div>
            <div className="text-[14px] text-ink font-mono font-medium">
              {system.load['1m'].toFixed(2)}{' '}
              <span className="text-mute">{system.load['5m'].toFixed(2)}</span>{' '}
              <span className="text-mute/60">{system.load['15m'].toFixed(2)}</span>
            </div>
          </div>
        )}
        {system.memory && (
          <div>
            <div className="text-xs font-bold text-mute/50 uppercase tracking-widest mb-1.5">Memory</div>
            <div className="text-[14px] text-ink font-medium">
              {(system.memory.used_mb / 1024).toFixed(1)}
              <span className="text-mute/60 font-normal"> / {(system.memory.total_mb / 1024).toFixed(1)} GB</span>
            </div>
          </div>
        )}
        {system.uptime_seconds !== undefined && (
          <div>
            <div className="text-xs font-bold text-mute/50 uppercase tracking-widest mb-1.5">System Uptime</div>
            <div className="text-[14px] text-ink font-medium">
              {formatSystemUptime(system.uptime_seconds)}
            </div>
          </div>
        )}
        <div>
          <div className="text-xs font-bold text-mute/50 uppercase tracking-widest mb-1.5">CPUs</div>
          <div className="text-[14px] text-ink font-medium">
            {system.cpus} <span className="text-mute/60 font-normal">{system.arch}</span>
          </div>
        </div>
      </div>
    </div>
  )
}

function HostStatsSection({ host, totalPanes }: { host: Host; totalPanes: number }) {
  const hostStats = host.stats as SystemStats | undefined
  if (!hostStats) return null

  const processes = hostStats.processes || []

  return (
    <div className="mb-10">
      <h3 className="text-ink text-[13px] font-bold uppercase tracking-widest mb-4 flex items-center gap-2">
        <span className={`w-1.5 h-1.5 rounded-full ${host.online ? 'bg-success' : 'bg-muted-foreground'}`} />
        {host.name}
      </h3>
      <div className="grid grid-cols-[repeat(auto-fit,minmax(360px,1fr))] gap-4">
        {processes.length > 0 && (
          <div>
            <div className="text-xs font-bold text-mute uppercase tracking-widest mb-2.5 ml-1">Processes</div>
            <div className="bg-surface border border-hairline rounded-lg p-5">
              <ProcessBar processes={processes} totalPanes={totalPanes} />
            </div>
          </div>
        )}
        <div>
          <div className="text-xs font-bold text-mute uppercase tracking-widest mb-2.5 ml-1">System</div>
          <SystemStatsCard system={hostStats} />
        </div>
      </div>
    </div>
  )
}

export function Overview({
  sessions,
  hosts,
  onSessionSelect,
  getSessionEvents,
  getSessionActivity,
  pendingAlerts,
  onJumpToSession,
  onDismissAlert,
}: OverviewProps) {
  const [stats, setStats] = useState<Stats | null>(null)
  const { prefs } = usePreferences()
  const hasMultipleHosts = hosts.length > 1

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

  return (
    <div className="flex-1 p-8 overflow-y-auto font-sans text-sm font-medium bg-canvas">
      {/* Stat cards */}
      <div className="flex gap-4 mb-10 flex-wrap">
        {hasMultipleHosts && (
          <StatCard
            label="Hosts"
            value={hosts.filter(h => h.online).length}
            sub={`${hosts.length} total`}
            color="var(--chart-primary)"
          />
        )}
        <StatCard
          label="Sessions"
          value={stats?.sessions.total ?? sessions.length}
          sub={stats ? `${stats.sessions.attached} attached` : undefined}
        />
        <StatCard
          label="Windows"
          value={stats?.windows ?? sessions.reduce((n, s) => n + (s.windows?.length || 0), 0)}
        />
        <StatCard
          label="Panes"
          value={stats?.panes ?? '—'}
          sub={stats && stats.agent_panes > 0 ? `${stats.agent_panes} agents` : undefined}
        />
        <StatCard
          label="Agents"
          value={stats?.agents.active ?? 0}
          color="var(--success)"
        />
        <StatCard
          label="Waiting"
          value={stats?.agents.waiting ?? 0}
          color={stats && stats.agents.waiting > 0 ? 'var(--warning)' : 'var(--muted-foreground)'}
        />
      </div>

      {/* Pending alerts */}
      {pendingAlerts.length > 0 && (
        <div className="mb-10">
          <h3 className="text-ink text-[13px] font-bold uppercase tracking-widest mb-4">
            Pending Alerts ({pendingAlerts.length})
          </h3>
          <div className="flex flex-col gap-2">
            {pendingAlerts.map((evt, i) => {
              const cfg = statusConfig[evt.status] || { color: 'var(--muted-foreground)', label: evt.status, bg: 'transparent' }
              const tc = toolColors[evt.tool] || 'var(--muted-foreground)'
              return (
                <div
                  key={`${evt.tool}-${evt.session}-${evt.pane}-${i}`}
                  className="bg-surface rounded-md p-3 px-4 flex items-center gap-3 cursor-pointer transition-all hover:bg-surface-elevated border-hairline"
                  style={{
                    border: `1px solid color-mix(in oklch, ${cfg.color} 15%, transparent)`,
                    borderLeft: `3px solid ${cfg.color}`,
                  }}
                  onClick={() => onJumpToSession(evt.host ? `${evt.host}/${evt.session}` : evt.session, evt.window, evt.pane)}
                >
                  <span
                    className={`w-1.5 h-1.5 rounded-full shrink-0 ${evt.status === 'waiting' || evt.status === 'stuck' ? 'animate-[pulse_1.5s_ease-in-out_infinite]' : ''}`}
                    style={{ background: cfg.color }}
                  />
                  <span className="font-bold text-[13px] tracking-tight" style={{ color: tc }}>{evt.tool.toUpperCase()}</span>
                  <span className="text-xs font-bold" style={{ color: cfg.color }}>{cfg.label.toUpperCase()}</span>
                  <span className="text-mute/60 text-xs font-bold tracking-widest uppercase px-1">IN</span>
                  <span className="text-ink font-bold text-[13px]">{evt.host_name ? `${evt.host_name}: ` : ''}{evt.session}</span>
                  {evt.message && (
                    <span className="text-mute/50 text-xs font-medium flex-1 overflow-hidden text-ellipsis whitespace-nowrap italic ml-2">
                      — {evt.message}
                    </span>
                  )}
                  <span
                    onClick={(e) => { e.stopPropagation(); onDismissAlert(evt) }}
                    className="text-mute/30 text-xl cursor-pointer leading-none hover:text-ink transition-colors"
                  >×</span>
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* Sessions grid */}
      {sessions.length === 0 && (
        <div className="text-mute text-[13px] font-medium mb-10 ml-1">
          No tmux sessions found. Start a tmux session to get started.
        </div>
      )}
      {(() => {
        const groups = new Map<string, Session[]>()
        for (const s of sessions) {
          const label = hasMultipleHosts ? (s.host_name || 'Local') : 'Sessions'
          if (!groups.has(label)) groups.set(label, [])
          groups.get(label)!.push(s)
        }
        const sortedGroups = Array.from(groups.entries()).sort(([a], [b]) =>
          a === 'Local' || a === 'Sessions' ? -1 : b === 'Local' || b === 'Sessions' ? 1 : a.localeCompare(b)
        )
        return sortedGroups.map(([groupLabel, groupSessions]) => (
          <div key={groupLabel} className="mb-10">
            <h3 className="text-ink text-[13px] font-bold uppercase tracking-widest mb-4 flex items-center gap-2">
              {hasMultipleHosts && (
                <span className={`w-1.5 h-1.5 rounded-full ${
                  groupSessions[0]?.host_online !== false ? 'bg-success' : 'bg-muted-foreground'
                }`} />
              )}
              {groupLabel}
              <span className="text-mute/40 font-bold text-xs ml-1">({groupSessions.length})</span>
            </h3>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(340px,1fr))] gap-4">
              {groupSessions.map((session) => {
                const sk = sessionKey(session)
                const events = getSessionEvents(sk)
                const hasWaiting = events.some(e => e.status === 'waiting' || e.status === 'stuck')
                const act = getSessionActivity(sk)
                const active = isSessionActive(session)
                const isOffline = session.host && session.host_online === false
                const eventPanes = new Set(events.map(e => e.pane).filter(Boolean))
                const agentCount = (session.windows || []).reduce((n, w) =>
                  n + (w.panes || []).filter(p => agentCommands.has(p.current_command) || eventPanes.has(p.id)).length, 0)

                return (
                  <div
                    key={sk}
                    onClick={() => onSessionSelect(session)}
                    className={`bg-surface rounded-lg p-5 cursor-pointer transition-all hover:border-hairline/60 border border-hairline group ${isOffline ? 'opacity-60' : ''}`}
                    style={{
                      borderColor: hasWaiting ? 'var(--warning)' : undefined,
                    }}
                  >
                    {/* Header row */}
                    <div className="flex items-center gap-2 mb-3">
                      <span className="text-[15px] font-bold tracking-tight text-ink group-hover:text-primary transition-colors">{session.name}</span>
                      {session.attached && (
                        <span className="text-[9px] font-bold uppercase tracking-wider text-success px-1.5 py-[1.5px] rounded-sm bg-success/10 border border-success/20">
                          attached
                        </span>
                      )}
                      <span className="ml-auto text-xs font-bold text-mute/40 uppercase tracking-wider">
                        {formatUptime(session.created, prefs.timestamp_format)}
                      </span>
                    </div>

                    {/* Stats row */}
                    <div className="flex items-center gap-4 mb-3 text-xs font-bold text-mute/70 uppercase tracking-wide">
                      <span className="flex items-center gap-1.5">
                        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="18" height="18" rx="2" ry="2"/></svg>
                        {session.windows?.length || 0}
                      </span>
                      {agentCount > 0 && (
                        <span className="flex items-center gap-1.5" style={{ color: toolColors.claude }}>
                          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/></svg>
                          {agentCount}
                        </span>
                      )}
                      {active ? (
                        <span className="text-success flex items-center gap-1.5">
                          <span className="w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
                          ACTIVE
                        </span>
                      ) : (
                        <span className="text-mute/40 font-bold uppercase">IDLE</span>
                      )}
                    </div>

                    {/* Sparkline */}
                    {prefs.sparklines_visible && act && act.sparkline && (
                      <div className="mt-4 pt-4 border-t border-hairline/40">
                        <Sparkline data={act.sparkline} height={20} />
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        ))
      })()}

      {/* Per-host processes + system stats */}
      {hasMultipleHosts ? (
        hosts.filter(h => h.stats).map(host => {
          const paneCount = (host.sessions || []).reduce((n, s: any) =>
            n + (s.windows || []).reduce((wn: number, w: any) => wn + (w.panes || []).length, 0), 0)
          return <HostStatsSection key={host.id} host={host} totalPanes={paneCount} />
        })
      ) : (
        <div className="grid grid-cols-[repeat(auto-fit,minmax(360px,1fr))] gap-4">
          {stats && stats.processes && stats.processes.length > 0 && (
            <div>
              <h3 className="text-ink text-[13px] font-bold uppercase tracking-widest mb-4 ml-1">Processes</h3>
              <div className="bg-surface border border-hairline rounded-lg p-5">
                <ProcessBar processes={stats.processes} totalPanes={stats.panes} />
              </div>
            </div>
          )}

          {stats?.system && (
            <div>
              <h3 className="text-ink text-[13px] font-bold uppercase tracking-widest mb-4 ml-1">System</h3>
              <SystemStatsCard system={stats.system} />
            </div>
          )}
        </div>
      )}
    </div>
  )
}
