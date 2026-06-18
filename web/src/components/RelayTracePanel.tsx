import { useState, useEffect, useCallback } from 'react'
import { fetchTrace, clearTrace, type RelayTraceEvent } from '../lib/relayTrace'

// Debug overlay for the remote-PTY relay timeline. Toggle with Ctrl+Alt+L or
// the URL param ?debug=relay. Shows this host's ring buffer (viewer + browser
// events). Listener-side events (first-read) live on the other host's buffer;
// grab those from its /api/debug/relay-trace endpoint.
export function RelayTracePanel() {
  const [open, setOpen] = useState(() => {
    try {
      return new URLSearchParams(window.location.search).get('debug') === 'relay'
    } catch {
      return false
    }
  })
  const [events, setEvents] = useState<RelayTraceEvent[]>([])
  const [host, setHost] = useState('')
  const [err, setErr] = useState('')
  const [auto, setAuto] = useState(true)

  const refresh = useCallback(async () => {
    try {
      const dump = await fetchTrace()
      setEvents(dump.events)
      setHost(dump.host)
      setErr('')
    } catch (e) {
      setErr(String(e))
    }
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey && e.altKey && (e.key === 'l' || e.key === 'L')) {
        e.preventDefault()
        setOpen((o) => !o)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  useEffect(() => {
    if (!open) return
    refresh()
    if (!auto) return
    const t = window.setInterval(refresh, 1000)
    return () => window.clearInterval(t)
  }, [open, auto, refresh])

  if (!open) return null

  const copy = () => {
    const text = JSON.stringify({ host, events }, null, 2)
    navigator.clipboard?.writeText(text).catch(() => {})
  }

  // Color-code the events that matter for the read-vs-deliver question.
  const tone = (ev: string): string => {
    if (ev === 'deliver-no-binding' || ev.includes('error') || ev.includes('drop') || ev.includes('fail') || ev === 'never-painted') return '#f87171'
    if (ev === 'first-read' || ev === 'deliver-first' || ev === 'first-byte') return '#4ade80'
    if (ev === 'peer-link-down') return '#fbbf24'
    return '#94a3b8'
  }

  return (
    <div
      style={{
        position: 'fixed', right: 12, bottom: 12, width: 560, maxHeight: '70vh',
        background: '#0b0f14', color: '#e2e8f0', border: '1px solid #1e293b',
        borderRadius: 8, zIndex: 9999, display: 'flex', flexDirection: 'column',
        font: '12px ui-monospace, monospace', boxShadow: '0 8px 32px rgba(0,0,0,.5)',
      }}
    >
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '8px 10px', borderBottom: '1px solid #1e293b' }}>
        <strong style={{ flex: 1 }}>relay-trace · {host || '?'} · {events.length}</strong>
        <label style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
          <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} /> auto
        </label>
        <button onClick={refresh} style={btn}>refresh</button>
        <button onClick={copy} style={btn}>copy</button>
        <button onClick={() => { clearTrace(); setEvents([]) }} style={btn}>clear</button>
        <button onClick={() => setOpen(false)} style={btn}>×</button>
      </div>
      {err && <div style={{ color: '#f87171', padding: '6px 10px' }}>{err}</div>}
      <div style={{ overflow: 'auto', padding: '4px 0' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <tbody>
            {events.map((e, i) => {
              const prev = events[i - 1]
              const dt = prev && e.unix_us && prev.unix_us ? (e.unix_us - prev.unix_us) / 1000 : 0
              return (
                <tr key={i} style={{ borderBottom: '1px solid #111827' }}>
                  <td style={{ ...cell, color: '#64748b', whiteSpace: 'nowrap' }}>{e.iso}</td>
                  <td style={{ ...cell, color: '#64748b', textAlign: 'right' }}>{dt ? `+${dt.toFixed(0)}ms` : ''}</td>
                  <td style={{ ...cell, color: '#7dd3fc' }}>{e.side?.[0]}</td>
                  <td style={{ ...cell, color: tone(e.event), fontWeight: 600 }}>{e.event}</td>
                  <td style={{ ...cell, color: '#cbd5e1' }}>{e.session || e.stream?.slice(0, 8) || ''}</td>
                  <td style={{ ...cell, color: '#64748b' }}>{e.bytes ? `${e.bytes}b` : ''} {e.detail || ''}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}

const btn: React.CSSProperties = {
  background: '#1e293b', color: '#e2e8f0', border: 'none', borderRadius: 4,
  padding: '2px 8px', cursor: 'pointer', font: 'inherit',
}
const cell: React.CSSProperties = { padding: '2px 6px', verticalAlign: 'top' }
