import { useState, useEffect, useCallback } from 'react'
import { themePresets, applyTheme } from '../theme'
import { usePreferences } from '../hooks/usePreferences'
import { usePushNotifications } from '../hooks/usePushNotifications'
import { cn } from '../lib/utils'

interface AgentStatus {
  name: string
  key: string
  installed: boolean
  configured: boolean
}

interface StatusResult {
  agents: AgentStatus[]
  setup_command: string
}

export function AgentStatusList({ agents }: { agents: AgentStatus[] }) {
  return (
    <div className="space-y-2">
      {agents.map(agent => (
        <div key={agent.key} className="flex items-center justify-between py-3 px-4 rounded-lg border border-hairline bg-surface transition-all hover:border-hairline/60">
          <span className="text-[13px] font-bold text-ink tracking-tight">{agent.name}</span>
          <div className="flex items-center gap-3 text-xs font-bold uppercase tracking-widest">
            <span className={agent.installed ? 'text-success' : 'text-mute/40'}>
              {agent.installed ? 'INSTALLED' : 'NOT FOUND'}
            </span>
            {agent.installed && (
              <span className={agent.configured ? 'text-success' : 'text-warning'}>
                {agent.configured ? 'READY' : 'SETUP REQUIRED'}
              </span>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}

export function SetupCommandBox({ command }: { command: string }) {
  const [copied, setCopied] = useState(false)

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(command).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }, [command])

  return (
    <button
      onClick={handleCopy}
      className="w-full flex items-center justify-between px-4 py-3.5 rounded border border-hairline bg-surface-elevated text-ink font-mono text-[13px] hover:border-primary/40 transition-all cursor-pointer group"
    >
      <span className="opacity-90">$ {command}</span>
      <span className="text-xs font-bold uppercase tracking-widest text-primary opacity-0 group-hover:opacity-100 transition-opacity">
        {copied ? 'COPIED!' : 'CLICK TO COPY'}
      </span>
    </button>
  )
}

export function Setup({ onComplete, fullPage = false }: { onComplete: () => void; fullPage?: boolean }) {
  const [status, setStatus] = useState<StatusResult | null>(null)
  const [loading, setLoading] = useState(true)
  const { prefs, updatePrefs } = usePreferences()
  const { pushState, subscribe: pushSubscribe } = usePushNotifications()
  const [step, setStep] = useState<'agents' | 'preferences'>('agents')

  const fetchStatus = useCallback(async () => {
    setLoading(true)
    try {
      const res = await fetch('/api/agent-status')
      if (res.ok) {
        setStatus(await res.json())
      }
    } catch {
      // ignore
    }
    setLoading(false)
  }, [])

  useEffect(() => {
    fetchStatus()
  }, [fetchStatus])

  const allConfigured = status?.agents.every(a => !a.installed || a.configured) ?? false

  const handleThemeChange = async (themeName: string) => {
    applyTheme(themeName, prefs.custom_theme)
    await updatePrefs({ theme: themeName })
  }

  return (
    <div className={fullPage ? "flex items-center justify-center min-h-full w-full bg-canvas py-10" : "flex-1 flex items-center justify-center overflow-y-auto"}>
      <div className="w-full max-w-md p-10 bg-surface border border-hairline rounded-xl">
        <div className="text-center mb-10">
          <h1 className="text-xl font-bold text-ink tracking-tight uppercase tracking-[0.15em]">
            {step === 'agents' ? 'Agent Setup' : 'Preferences'}
          </h1>
          <p className="text-[13px] font-medium text-mute/60 mt-4 leading-relaxed">
            {step === 'agents'
              ? 'Configure your agents to report status to GUPPI'
              : 'Pick a theme and enable system notifications'}
          </p>
        </div>

        {step === 'agents' && (
          <>
            {loading && !status ? (
              <div className="text-center text-xs font-bold uppercase tracking-widest text-mute/40 py-10 animate-pulse">Checking status...</div>
            ) : status ? (
              <div className="space-y-8">
                <AgentStatusList agents={status.agents} />

                {!allConfigured && (
                  <div className="space-y-4">
                    <p className="text-xs font-bold text-mute uppercase tracking-widest ml-1">
                      Configuration Command
                    </p>
                    <SetupCommandBox command={status.setup_command} />
                  </div>
                )}

                {allConfigured && (
                  <div className="py-4 px-4 bg-success/5 border border-success/20 rounded-lg text-center">
                    <p className="text-xs font-bold text-success uppercase tracking-widest">
                      All agents configured
                    </p>
                  </div>
                )}

                <div className="flex gap-4 pt-2">
                  <button
                    onClick={fetchStatus}
                    disabled={loading}
                    className="flex-1 px-4 py-3 rounded-full text-[13px] font-bold uppercase tracking-widest border border-hairline bg-surface text-ink hover:bg-surface-elevated transition-all disabled:opacity-50"
                  >
                    {loading ? 'WAIT...' : 'REFRESH'}
                  </button>
                  <button
                    onClick={() => setStep('preferences')}
                    className="flex-1 px-4 py-3 bg-primary text-primary-foreground rounded-full text-[13px] font-bold uppercase tracking-widest hover:bg-white/90 transition-all"
                  >
                    NEXT
                  </button>
                </div>

                <p className="text-xs font-medium text-mute/40 text-center leading-relaxed">
                  Multi-host? Run the setup command on each machine where you use agents.
                </p>
              </div>
            ) : (
              <div className="text-center">
                <p className="text-[13px] font-medium text-mute mb-6">Could not check agent status.</p>
                <button
                  onClick={() => setStep('preferences')}
                  className="w-full px-4 py-3 bg-primary text-primary-foreground rounded-full text-[13px] font-bold uppercase tracking-widest hover:bg-white/90 transition-all"
                >
                  NEXT
                </button>
              </div>
            )}
          </>
        )}

        {step === 'preferences' && (
          <div className="space-y-8">
            {/* Theme picker */}
            <div>
              <h3 className="text-xs font-bold text-mute uppercase tracking-widest mb-4 ml-1">Theme</h3>
              <div className="grid grid-cols-2 gap-3">
                {Object.values(themePresets).map(theme => (
                  <button
                    key={theme.name}
                    onClick={() => handleThemeChange(theme.name)}
                    className={cn(
                      'p-4 rounded-lg border text-left transition-all duration-200',
                      prefs.theme === theme.name
                        ? 'border-primary bg-primary/5'
                        : 'border-hairline bg-surface-elevated/30 hover:border-hairline/60',
                    )}
                  >
                    <div className="flex items-center gap-2 mb-2.5">
                      <div className="w-3.5 h-3.5 rounded-full border border-hairline/40" style={{ background: theme.xterm.background }} />
                      <div className="w-3.5 h-3.5 rounded-full border border-hairline/40" style={{ background: theme.xterm.foreground }} />
                      <div className="w-3.5 h-3.5 rounded-full border border-hairline/40" style={{ background: theme.xterm.blue }} />
                      <div className="w-3.5 h-3.5 rounded-full border border-hairline/40" style={{ background: theme.xterm.green }} />
                    </div>
                    <div className={cn(
                      'text-xs font-bold tracking-tight',
                      prefs.theme === theme.name ? 'text-primary' : 'text-ink/80'
                    )}>{theme.label}</div>
                  </button>
                ))}
              </div>
            </div>

            {/* Push notifications */}
            <div>
              <h3 className="text-xs font-bold text-mute uppercase tracking-widest mb-4 ml-1">Notifications</h3>
              <div className="rounded-lg border border-hairline bg-surface p-4">
                {pushState === 'unsupported' ? (
                  <p className="text-xs font-medium text-mute/60 italic">
                    Requires HTTPS or localhost with a supported browser.
                  </p>
                ) : pushState === 'denied' ? (
                  <p className="text-xs font-medium text-destructive uppercase tracking-widest">
                    Blocked by browser settings
                  </p>
                ) : pushState === 'subscribed' ? (
                  <div className="flex items-center justify-center gap-2 py-1">
                    <span className="w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
                    <span className="text-success text-xs font-bold uppercase tracking-widest">Push Enabled</span>
                  </div>
                ) : (
                  <div className="flex items-center justify-between">
                    <div>
                      <p className="text-[13px] font-bold text-ink uppercase tracking-tight">Push Alerts</p>
                      <p className="text-xs font-medium text-mute/60 mt-1 uppercase tracking-wide">Receive agent alerts</p>
                    </div>
                    <button
                      onClick={pushSubscribe}
                      className="px-5 py-2 rounded-full text-xs font-bold uppercase tracking-widest bg-primary text-primary-foreground hover:bg-white/90 transition-all"
                    >
                      ENABLE
                    </button>
                  </div>
                )}
              </div>
            </div>

            {/* Navigation */}
            <div className="flex gap-4 pt-2">
              <button
                onClick={() => setStep('agents')}
                className="flex-1 px-4 py-3 rounded-full text-[13px] font-bold uppercase tracking-widest border border-hairline bg-surface text-ink hover:bg-surface-elevated transition-all"
              >
                BACK
              </button>
              <button
                onClick={onComplete}
                className="flex-1 px-4 py-3 bg-primary text-primary-foreground rounded-full text-[13px] font-bold uppercase tracking-widest hover:bg-white/90 transition-all"
              >
                FINISH
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
