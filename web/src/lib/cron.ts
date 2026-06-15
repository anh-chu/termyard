import cronstrue from 'cronstrue'

// describeCron turns a standard 5-field cron spec into human-readable text.
// Returns null when the spec is empty or unparseable, so callers can fall back
// to showing the raw spec.
export function describeCron(spec?: string): string | null {
  const trimmed = (spec ?? '').trim()
  if (!trimmed) return null
  try {
    return cronstrue.toString(trimmed, { throwExceptionOnParseError: true })
  } catch {
    return null
  }
}
