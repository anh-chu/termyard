// time.ts centralizes session/schedule time formatting so display strings
// stay consistent across components. Each formatter reproduces the exact
// output of the original inline implementation it replaced.

// formatRelativeTime renders a signed, coarse relative time ("3m ago",
// "in 2h"). Used by Sidebar and ScheduleModal (was byte-identical in both).
export function formatRelativeTime(iso?: string): string {
  if (!iso) return '—'
  const ts = new Date(iso).getTime()
  if (!Number.isFinite(ts)) return '—'
  const diff = ts - Date.now()
  const future = diff > 0
  const abs = Math.abs(diff)
  const mins = Math.round(abs / 60000)
  if (mins < 1) return future ? 'now' : 'just now'
  if (mins < 60) return future ? `in ${mins}m` : `${mins}m ago`
  const hours = Math.round(mins / 60)
  if (hours < 24) return future ? `in ${hours}h` : `${hours}h ago`
  const days = Math.round(hours / 24)
  return future ? `in ${days}d` : `${days}d ago`
}

// formatUptime renders elapsed time since `created` as a compact badge.
// Sidebar variant: guards undefined/invalid/negative -> '', and uses 'now'
// for sub-minute. Do NOT merge with formatSessionUptime; outputs differ.
export function formatUptime(created?: string): string {
  if (!created) return ''
  const ms = Date.now() - new Date(created).getTime()
  if (!Number.isFinite(ms) || ms < 0) return ''
  const minutes = Math.floor(ms / 60000)
  if (minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  return `${days}d`
}

// formatSessionUptime is the Overview variant: supports an 'absolute' format
// (wall-clock time) and has no undefined/negative guards (sub-minute -> '0m').
// Intentionally distinct from formatUptime; behaviors must not be merged.
export function formatSessionUptime(created: string, format: string = 'relative'): string {
  if (format === 'absolute') return new Date(created).toLocaleTimeString()
  const diff = Date.now() - new Date(created).getTime()
  const hours = Math.floor(diff / 3600000)
  if (hours < 1) return `${Math.floor(diff / 60000)}m`
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

// formatSystemUptime renders host uptime from a seconds count ("3d 4h").
export function formatSystemUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const mins = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${mins}m`
  return `${mins}m`
}

// formatRunCount pluralizes a schedule run count ("1 run", "3 runs").
export function formatRunCount(count: number): string {
  return `${count} run${count === 1 ? '' : 's'}`
}
