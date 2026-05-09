import { useState, useEffect, useCallback } from 'react'
import { usePreferences, type Preferences } from '../hooks/usePreferences'
import { usePushNotifications } from '../hooks/usePushNotifications'
import { themePresets, applyTheme } from '../theme'
import { cn } from '../lib/utils'
import { AgentStatusList, SetupCommandBox } from './Setup'

const terminalFontFamilies = [
  'Space Mono',
  'JetBrains Mono',
  'Fira Code',
  'Menlo',
  'Monaco',
  'Consolas',
  'Courier New',
  'monospace',
]

const timestampFormats = [
  { value: 'relative', label: 'Relative (2m ago)' },
  { value: 'absolute', label: 'Absolute (14:32:05)' },
]

const shortcutOptions = [
  { value: 'ctrl+k', label: 'Ctrl+K / Cmd+K' },
  { value: 'ctrl+p', label: 'Ctrl+P / Cmd+P' },
  { value: 'ctrl+space', label: 'Ctrl+Space' },
]

const notifStatuses = [
  { value: 'waiting', label: 'Waiting' },
  { value: 'error', label: 'Error' },
  { value: 'completed', label: 'Completed' },
]

// Customizable CSS variables exposed to users, grouped by purpose
const customizableVars = [
  { key: '--primary', label: 'Accent' },
  { key: '--background', label: 'Background' },
  { key: '--foreground', label: 'Foreground' },
  { key: '--border', label: 'Border' },
  { key: '--card', label: 'Card' },
  { key: '--muted', label: 'Muted' },
  { key: '--success', label: 'Success' },
  { key: '--warning', label: 'Warning' },
  { key: '--destructive', label: 'Destructive' },
]

const sectionIds = ['appearance', 'terminal', 'interface', 'notifications', 'agents', 'security'] as const

function Section({ id, title, description, children }: { id: string; title: string; description?: string; children: React.ReactNode }) {
  return (
    <section id={id} className="rounded-lg border border-hairline bg-surface p-6 scroll-mt-6">
      <h3 className="text-[13px] font-bold text-ink mb-1 uppercase tracking-widest">{title}</h3>
      {description && <p className="text-xs font-medium text-mute/60 mb-5">{description}</p>}
      {!description && <div className="mb-5" />}
      <div className="flex flex-col gap-4">
        {children}
      </div>
    </section>
  )
}

function Divider() {
  return <div className="border-t border-hairline/40 -mx-6 my-1" />
}

function Row({ label, description, children }: { label: string; description?: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-6 py-1">
      <div className="flex-1">
        <div className="text-[13px] font-semibold text-ink tracking-tight">{label}</div>
        {description && <div className="text-xs font-medium text-mute/50 mt-1">{description}</div>}
      </div>
      <div className="shrink-0">
        {children}
      </div>
    </div>
  )
}

function SelectInput({ value, onChange, options }: { value: string; onChange: (v: string) => void; options: { value: string; label: string }[] }) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="bg-surface-elevated border border-hairline rounded-sm px-3 py-1.5 text-[13px] font-medium text-ink outline-none focus:border-primary/60 min-w-[180px] transition-colors cursor-pointer"
    >
      {options.map(o => (
        <option key={o.value} value={o.value}>{o.label}</option>
      ))}
    </select>
  )
}

function NumberInput({ value, onChange, min, max, step }: { value: number; onChange: (v: number) => void; min?: number; max?: number; step?: number }) {
  return (
    <input
      type="number"
      value={value}
      onChange={(e) => onChange(Number(e.target.value))}
      min={min}
      max={max}
      step={step}
      className="bg-surface-elevated border border-hairline rounded-sm px-3 py-1.5 text-[13px] font-medium text-ink outline-none focus:border-primary/60 w-[80px] text-right"
    />
  )
}

function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label?: string }) {
  return (
    <div className="flex items-center gap-3">
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={cn(
          'inline-flex h-5 w-9 shrink-0 items-center rounded-full border border-transparent transition-all duration-200',
          checked ? 'bg-primary' : 'bg-surface-elevated border-hairline',
        )}
      >
        <span
          className={cn(
            'pointer-events-none block h-3.5 w-3.5 rounded-full transition-transform duration-200 mx-0.5',
            checked ? 'translate-x-4 bg-primary-foreground' : 'translate-x-0 bg-muted-foreground/60',
          )}
        />
      </button>
      {label && <span className="text-xs font-bold uppercase tracking-wider text-mute/60">{label}</span>}
    </div>
  )
}

function ColorInput({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <input
      type="color"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="w-8 h-6 rounded border border-hairline cursor-pointer bg-transparent"
    />
  )
}

// Resolve a CSS variable value (possibly oklch) to a hex color for the color picker
function resolvedColor(cssVar: string, customTheme: Record<string, string>): string {
  const override = customTheme[cssVar]
  if (override && override.startsWith('#')) return override
  // Read from computed style
  const raw = getComputedStyle(document.documentElement).getPropertyValue(cssVar).trim()
  if (raw.startsWith('#')) return raw
  // Convert oklch/rgb via a temp element
  const el = document.createElement('div')
  el.style.color = raw
  document.body.appendChild(el)
  const computed = getComputedStyle(el).color
  document.body.removeChild(el)
  // Parse rgb(r, g, b)
  const match = computed.match(/(\d+),\s*(\d+),\s*(\d+)/)
  if (match) {
    const hex = (n: number) => n.toString(16).padStart(2, '0')
    return `#${hex(+match[1])}${hex(+match[2])}${hex(+match[3])}`
  }
  return '#888888'
}

const sectionLabels: Record<typeof sectionIds[number], string> = {
  appearance: 'Appearance',
  terminal: 'Terminal',
  interface: 'Interface',
  notifications: 'Notifications',
  agents: 'Agents',
  security: 'Security',
}

export function Settings({ pushState, onPushSubscribe, onPushUnsubscribe, onLogout }: {
  pushState: string
  onPushSubscribe: () => void
  onPushUnsubscribe: () => void
  onLogout?: () => void
}) {
  const { prefs, updatePrefs } = usePreferences()
  const [saving, setSaving] = useState(false)
  const [showCustomColors, setShowCustomColors] = useState(() => Object.keys(prefs.custom_theme || {}).length > 0)
  const [agentStatus, setAgentStatus] = useState<{ agents: { name: string; key: string; installed: boolean; configured: boolean }[]; setup_command: string } | null>(null)
  const [agentLoading, setAgentLoading] = useState(false)

  const fetchAgentStatus = useCallback(async () => {
    setAgentLoading(true)
    try {
      const res = await fetch('/api/agent-status')
      if (res.ok) setAgentStatus(await res.json())
    } catch {}
    setAgentLoading(false)
  }, [])

  useEffect(() => {
    fetchAgentStatus()
  }, [fetchAgentStatus])

  const update = async (partial: Partial<Preferences>) => {
    setSaving(true)
    await updatePrefs(partial)
    setSaving(false)
  }

  const updateNested = async <K extends keyof Preferences>(
    key: K,
    nested: Partial<Preferences[K]>,
  ) => {
    const current = prefs[key]
    await update({ [key]: { ...(typeof current === 'object' ? current : {}), ...nested } } as Partial<Preferences>)
  }

  const handleThemeChange = async (theme: string) => {
    applyTheme(theme, prefs.custom_theme)
    await update({ theme })
  }

  const handleCustomColorChange = async (cssVar: string, hex: string) => {
    const next = { ...(prefs.custom_theme || {}), [cssVar]: hex }
    applyTheme(prefs.theme, next)
    await update({ custom_theme: next })
  }

  const handleResetCustomColors = async () => {
    applyTheme(prefs.theme, {})
    await update({ custom_theme: {} })
    setShowCustomColors(false)
  }

  const toggleNotifStatus = async (status: string) => {
    const current = prefs.notifications.statuses
    const next = current.includes(status)
      ? current.filter(s => s !== status)
      : [...current, status]
    await updateNested('notifications', { statuses: next })
  }

  const visibleSections = onLogout ? sectionIds : sectionIds.filter(s => s !== 'security')
  const customTheme = prefs.custom_theme || {}
  const hasCustomColors = Object.values(customTheme).some(v => !!v)

  return (
    <div className="flex-1 p-10 overflow-y-auto font-sans text-[13px] font-medium bg-canvas scroll-smooth">
      <div className="max-w-2xl mx-auto">
        <div className="flex items-center justify-between mb-8">
          <h2 className="text-xl font-bold text-ink tracking-tight uppercase tracking-[0.1em]">Settings</h2>
          {saving && <span className="text-xs font-bold text-primary animate-pulse">SAVING...</span>}
        </div>

        {/* Jump nav */}
        <nav className="flex gap-1.5 mb-8 flex-wrap">
          {visibleSections.map(id => (
            <a
              key={id}
              href={`#${id}`}
              className="px-3 py-1.5 rounded-sm text-xs font-bold uppercase tracking-wider text-mute/60 hover:text-ink hover:bg-surface-elevated transition-all"
            >
              {sectionLabels[id]}
            </a>
          ))}
        </nav>

        <div className="flex flex-col gap-6">
          {/* ── Appearance ── */}
          <Section id="appearance" title="Appearance" description="Theme, colors, fonts, and display formatting">
            <div className="grid grid-cols-2 gap-3">
              {Object.values(themePresets).map(theme => (
                <button
                  key={theme.name}
                  onClick={() => handleThemeChange(theme.name)}
                  className={cn(
                    'p-4 rounded-lg border text-left transition-all duration-200 group',
                    prefs.theme === theme.name
                      ? 'border-primary bg-primary/5 shadow-[0_0_15px_rgba(255,255,255,0.05)]'
                      : 'border-hairline bg-surface hover:border-hairline/60',
                  )}
                >
                  <div className="flex items-center gap-2 mb-3">
                    <div className="w-4.5 h-4.5 rounded-full border border-hairline/40" style={{ background: theme.xterm.background }} />
                    <div className="w-4.5 h-4.5 rounded-full border border-hairline/40" style={{ background: theme.xterm.foreground }} />
                    <div className="w-4.5 h-4.5 rounded-full border border-hairline/40" style={{ background: theme.xterm.blue }} />
                    <div className="w-4.5 h-4.5 rounded-full border border-hairline/40" style={{ background: theme.xterm.green }} />
                  </div>
                  <div className={cn(
                    'text-[13px] font-bold tracking-tight',
                    prefs.theme === theme.name ? 'text-primary' : 'text-ink/80'
                  )}>{theme.label}</div>
                </button>
              ))}
            </div>

            <Divider />

            {/* Custom color overrides */}
            <div className="py-1">
              <button
                onClick={() => setShowCustomColors(v => !v)}
                className="text-xs font-bold uppercase tracking-widest text-mute/60 hover:text-ink transition-colors flex items-center gap-2"
              >
                <span className={cn('inline-block transition-transform text-xs', showCustomColors && 'rotate-90')}>▶</span>
                Custom Colors
                {hasCustomColors && <span className="text-xs text-primary">(ACTIVE)</span>}
              </button>
              {showCustomColors && (
                <div className="mt-4 flex flex-col gap-3">
                  <div className="grid grid-cols-3 gap-3">
                    {customizableVars.map(({ key, label }) => (
                      <div key={key} className="flex items-center gap-3">
                        <ColorInput
                          value={resolvedColor(key, customTheme)}
                          onChange={(hex) => handleCustomColorChange(key, hex)}
                        />
                        <span className="text-xs font-bold text-mute/60 uppercase tracking-tight">{label}</span>
                      </div>
                    ))}
                  </div>
                  {hasCustomColors && (
                    <button
                      onClick={handleResetCustomColors}
                      className="text-xs font-bold uppercase tracking-widest text-mute/40 hover:text-destructive transition-colors self-start mt-1"
                    >
                      Reset to defaults
                    </button>
                  )}
                </div>
              )}
            </div>

            <Divider />

            <Row label="Timestamp Format" description="How timestamps are shown in the UI">
              <SelectInput
                value={prefs.timestamp_format}
                onChange={(v) => update({ timestamp_format: v })}
                options={timestampFormats}
              />
            </Row>
            <Row label="Overview Refresh" description="Stats refresh interval in seconds">
              <NumberInput
                value={prefs.overview_refresh_interval}
                onChange={(v) => update({ overview_refresh_interval: Math.max(1, Math.min(60, v)) })}
                min={1}
                max={60}
              />
            </Row>
          </Section>

          {/* ── Terminal ── */}
          <Section id="terminal" title="Terminal" description="Font, scrollback, and fullscreen behavior">
            <Row label="Font Family" description="Monospace font for the terminal">
              <SelectInput
                value={prefs.terminal.font_family}
                onChange={(v) => updateNested('terminal', { font_family: v })}
                options={terminalFontFamilies.map(f => ({ value: f, label: f }))}
              />
            </Row>
            <Row label="Font Size" description="Terminal text size in pixels">
              <NumberInput
                value={prefs.terminal.font_size}
                onChange={(v) => updateNested('terminal', { font_size: Math.max(8, Math.min(32, v)) })}
                min={8}
                max={32}
              />
            </Row>
            <Row label="Scrollback" description="Number of lines to keep in history">
              <NumberInput
                value={prefs.terminal.scrollback}
                onChange={(v) => updateNested('terminal', { scrollback: Math.max(100, Math.min(100000, v)) })}
                min={100}
                max={100000}
                step={500}
              />
            </Row>
            <Divider />
            <Row label="Hide Alerts in Fullscreen" description="Hide the agent alert banner when terminal is fullscreen">
              <Toggle
                checked={prefs.fullscreen_hide_alerts}
                onChange={(v) => update({ fullscreen_hide_alerts: v })}
              />
            </Row>
          </Section>

          {/* ── Interface ── */}
          <Section id="interface" title="Interface" description="Layout, sidebar, and keyboard shortcuts">
            <Row label="Default View" description="View shown on launch">
              <SelectInput
                value={prefs.default_view}
                onChange={(v) => update({ default_view: v })}
                options={[
                  { value: 'overview', label: 'Overview' },
                  { value: 'last-session', label: 'Last Session' },
                ]}
              />
            </Row>
            <Row label="Sidebar Default" description="Sidebar state on load">
              <Toggle
                checked={prefs.sidebar.default_collapsed}
                onChange={(v) => updateNested('sidebar', { default_collapsed: v })}
                label={prefs.sidebar.default_collapsed ? 'COLLAPSED' : 'EXPANDED'}
              />
            </Row>
            <Row label="Sidebar Collapse Mode" description="How sidebar behaves when collapsed">
              <SelectInput
                value={prefs.sidebar.collapse_mode || 'small'}
                onChange={(v) => updateNested('sidebar', { collapse_mode: v })}
                options={[
                  { value: 'small', label: 'Narrow column' },
                  { value: 'hidden', label: 'Completely hidden' },
                ]}
              />
            </Row>
            <Row label="Sparklines" description="Show activity sparklines in sidebar">
              <Toggle
                checked={prefs.sparklines_visible}
                onChange={(v) => update({ sparklines_visible: v })}
              />
            </Row>
            <Divider />
            <Row label="Quick Switcher Shortcut" description="Keyboard shortcut to open quick switcher">
              <SelectInput
                value={prefs.quick_switcher_shortcut}
                onChange={(v) => update({ quick_switcher_shortcut: v })}
                options={shortcutOptions}
              />
            </Row>
          </Section>

          {/* ── Notifications ── */}
          <Section id="notifications" title="Notifications" description="Push alerts and agent event notifications">
            <Row label="Push Alerts" description={
              pushState === 'unsupported'
                ? 'Requires HTTPS or localhost with a supported browser'
                : pushState === 'denied'
                ? 'Blocked by browser — reset in browser site settings'
                : pushState === 'subscribed'
                ? 'Receiving push alerts for agent events'
                : 'Enable to receive alerts even when the tab is closed'
            }>
              {pushState === 'unsupported' ? (
                <span className="text-xs font-bold text-mute/40 uppercase tracking-widest">Unavailable</span>
              ) : pushState === 'denied' ? (
                <span className="text-xs font-bold text-destructive uppercase tracking-widest">Blocked</span>
              ) : (
                <Toggle
                  checked={pushState === 'subscribed'}
                  onChange={(v) => v ? onPushSubscribe() : onPushUnsubscribe()}
                />
              )}
            </Row>
            <Row label="Alert Statuses" description="Which agent statuses trigger alerts">
              <div className="flex gap-2">
                {notifStatuses.map(s => {
                  const isActive = prefs.notifications.statuses.includes(s.value)
                  return (
                    <button
                      key={s.value}
                      onClick={() => toggleNotifStatus(s.value)}
                      className={cn(
                        'px-3 py-1.5 rounded-sm text-xs font-bold uppercase tracking-widest border transition-all',
                        isActive
                          ? 'border-primary bg-primary text-primary-foreground'
                          : 'border-hairline bg-surface text-mute/60 hover:border-hairline/60 hover:text-ink',
                      )}
                    >
                      {s.label}
                    </button>
                  )
                })}
              </div>
            </Row>
            <Row label="Auto-dismiss" description="Seconds before alerts auto-dismiss (0 = manual)">
              <NumberInput
                value={prefs.agent_banner.auto_dismiss_seconds}
                onChange={(v) => updateNested('agent_banner', { auto_dismiss_seconds: Math.max(0, Math.min(300, v)) })}
                min={0}
                max={300}
              />
            </Row>
          </Section>

          {/* ── Agents ── */}
          <Section id="agents" title="Agents" description="Agent installation and hook configuration status">
            {agentStatus ? (
              <div className="flex flex-col gap-4">
                <AgentStatusList agents={agentStatus.agents} />
                <SetupCommandBox command={agentStatus.setup_command} />
                <button
                  onClick={fetchAgentStatus}
                  disabled={agentLoading}
                  className="self-start px-4 py-2 rounded-md text-xs font-bold uppercase tracking-widest border border-hairline bg-surface text-ink hover:bg-surface-elevated transition-all disabled:opacity-50"
                >
                  {agentLoading ? 'Checking...' : 'Refresh Status'}
                </button>
              </div>
            ) : (
              <p className="text-[13px] font-medium text-mute/60 italic">
                {agentLoading ? 'Checking agents...' : 'Could not load agent status.'}
              </p>
            )}
          </Section>

          {/* ── Security ── */}
          {onLogout && (
            <Section id="security" title="Security" description="Session locking and sign out">
              <Row label="Auto-lock Timeout" description="Sign out after idle inactivity (0 = disabled)">
                <div className="flex items-center gap-2">
                  <NumberInput
                    value={prefs.lock_timeout_minutes}
                    onChange={(v) => update({ lock_timeout_minutes: Math.max(0, Math.min(120, v)) })}
                    min={0}
                    max={120}
                  />
                  <span className="text-xs font-bold text-mute/40 uppercase tracking-widest">min</span>
                </div>
              </Row>
              <Row label="Lock Faster When Backgrounded" description="Use a shorter timeout when the tab is hidden or minimized">
                <Toggle
                  checked={prefs.lock_background_faster}
                  onChange={(v) => update({ lock_background_faster: v })}
                />
              </Row>
              {prefs.lock_background_faster && (
                <Row label="Background Timeout" description="Idle timeout when tab is hidden">
                  <div className="flex items-center gap-2">
                    <NumberInput
                      value={prefs.lock_background_minutes}
                      onChange={(v) => update({ lock_background_minutes: Math.max(1, Math.min(120, v)) })}
                      min={1}
                      max={120}
                    />
                    <span className="text-xs font-bold text-mute/40 uppercase tracking-widest">min</span>
                  </div>
                </Row>
              )}
              <Divider />
              <Row label="Sign Out" description="End your current session">
                <button
                  onClick={onLogout}
                  className="px-6 py-2.5 rounded-full text-[13px] font-bold uppercase tracking-widest border border-destructive/40 text-destructive hover:bg-destructive hover:text-white transition-all"
                >
                  Sign out
                </button>
              </Row>
            </Section>
          )}
        </div>
      </div>
    </div>
  )
}
