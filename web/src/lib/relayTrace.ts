// Browser-side relay tracing. Posts terminal-stream lifecycle events to the
// local server's /api/debug/relay-trace ring buffer so the backend timeline
// (viewer + listener) and the browser timeline live in one place. Correlation
// to backend streams is by session name + wall clock (the browser never sees
// the backend stream id).
//
// Three sinks per event: console (DevTools, copy from there), an in-browser
// ring (window.__relayLog() / window.__relayDump()), and the backend ring
// buffer (so the agent can pull both hosts in one curl). Fire-and-forget.

export interface RelayTraceEvent {
  unix_us?: number
  iso?: string
  host?: string
  side?: string
  stream?: string
  session?: string
  event: string
  bytes?: number
  detail?: string
}

// In-browser ring of EVERY event (including per-message), capped. Dump with
// window.__relayDump() in DevTools.
const RING_MAX = 8000
interface FeLogRow {
  t: number // performance.now() ms since page load
  iso: string
  event: string
  session?: string
  host?: string
  bytes?: number
  detail?: string
}
const ring: FeLogRow[] = []
function push(row: FeLogRow) {
  ring.push(row)
  if (ring.length > RING_MAX) ring.splice(0, ring.length - RING_MAX)
}
if (typeof window !== 'undefined') {
  ;(window as any).__relayLog = () => ring.slice()
  ;(window as any).__relayDump = () => {
    const txt = ring
      .map(r => `${r.iso} +${r.t.toFixed(0)}ms ${r.event} session=${r.session ?? ''} host=${r.host ?? ''} bytes=${r.bytes ?? ''} ${r.detail ?? ''}`)
      .join('\n')
    // eslint-disable-next-line no-console
    console.log(txt)
    return txt
  }
  ;(window as any).__relayClear = () => { ring.length = 0 }
}

function postBackend(body: RelayTraceEvent): void {
  try {
    fetch('/api/debug/relay-trace', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      keepalive: true,
    }).catch(() => {})
  } catch { /* ignored */ }
}

// feLog: full-fidelity event. Console + in-browser ring + backend buffer.
export function feLog(
  event: string,
  opts: { session?: string; host?: string; bytes?: number; detail?: string } = {},
): void {
  const iso = new Date().toISOString().slice(11, 23)
  push({ t: performance.now(), iso, event, session: opts.session, host: opts.host, bytes: opts.bytes, detail: opts.detail })
  // eslint-disable-next-line no-console
  console.log(`[relay] ${event}`, { session: opts.session, host: opts.host, bytes: opts.bytes, detail: opts.detail })
  postBackend({
    side: 'browser',
    event,
    session: opts.session,
    bytes: opts.bytes,
    detail: `host=${opts.host ?? 'local'}${opts.detail ? ' ' + opts.detail : ''}`,
  })
}

// feMsg: per-message event. In-browser ring only (+ throttled console). No
// backend POST — one fetch per PTY frame would itself flood the link.
export function feMsg(row: { session?: string; host?: string; bytes?: number; detail?: string; seq: number }): void {
  const iso = new Date().toISOString().slice(11, 23)
  push({ t: performance.now(), iso, event: 'msg', session: row.session, host: row.host, bytes: row.bytes, detail: `seq=${row.seq} ${row.detail ?? ''}` })
  if (row.seq <= 20 || row.seq % 100 === 0) {
    // eslint-disable-next-line no-console
    console.log(`[relay] msg seq=${row.seq} session=${row.session} bytes=${row.bytes} ${row.detail ?? ''}`)
  }
}

// Back-compat shim for existing call sites.
export function postTrace(
  event: string,
  opts: { session?: string; hostId?: string; bytes?: number; detail?: string } = {},
): void {
  feLog(event, { session: opts.session, host: opts.hostId, bytes: opts.bytes, detail: opts.detail })
}

export interface RelayTraceDump {
  host: string
  events: RelayTraceEvent[]
}

export async function fetchTrace(session?: string): Promise<RelayTraceDump> {
  const q = session ? `?session=${encodeURIComponent(session)}` : ''
  const res = await fetch(`/api/debug/relay-trace${q}`)
  if (!res.ok) throw new Error(`trace fetch failed: ${res.status}`)
  return res.json()
}

export async function clearTrace(): Promise<void> {
  await fetch('/api/debug/relay-trace', { method: 'DELETE' }).catch(() => {})
}
