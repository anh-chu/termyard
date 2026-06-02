import { useEffect, useRef, useState, useCallback } from 'react'

// Generate a stable per-tab client ID so we can ignore our own echoes when
// the server broadcasts layout-updated.
function makeClientId(): string {
  try {
    const existing = sessionStorage.getItem('guppi:client-id')
    if (existing) return existing
    const fresh = 'tab-' + Math.random().toString(36).slice(2, 10)
    sessionStorage.setItem('guppi:client-id', fresh)
    return fresh
  } catch {
    return 'tab-' + Math.random().toString(36).slice(2, 10)
  }
}

export const LAYOUT_CLIENT_ID = makeClientId()

// Keys whose values we mirror to the server-side layout store. The server
// is opaque about the shape — it just persists and broadcasts. Frontend
// owns the schema.
const LAYOUT_KEYS = [
  'guppi:pane-tree',
  'guppi:active-key',
  'guppi:saved-groups',
  'guppi:active-group-id',
  'guppi:active-group-name',
  'guppi:group-order',
  'guppi:sidebar-collapsed',
  'guppi:collapsed-groups',
  'guppi:background-sessions',
  'guppi:session-order',
  'guppi:hidden-sessions',
] as const

type LayoutPayload = Record<string, unknown>

type RemoteLayout = {
  data: Record<string, unknown> | null
  updated_at?: string
  updated_by?: string
}

function readLocalLayout(): LayoutPayload {
  const out: LayoutPayload = {}
  for (const k of LAYOUT_KEYS) {
    try {
      const raw = localStorage.getItem(k)
      if (raw == null) continue
      // Try to parse as JSON; fall back to string.
      try { out[k] = JSON.parse(raw) }
      catch { out[k] = raw }
    } catch {}
  }
  return out
}

function writeLocalLayout(data: LayoutPayload | null) {
  if (!data) return
  for (const k of LAYOUT_KEYS) {
    if (!(k in data)) continue
    try {
      const v = data[k]
      if (v == null) {
        localStorage.removeItem(k)
        continue
      }
      const raw = typeof v === 'string' ? v : JSON.stringify(v)
      localStorage.setItem(k, raw)
    } catch {}
  }
}

// useLayoutSync wires layout localStorage keys to the server-side layout
// store. It returns a `pushNow` function that schedules a debounced PUT, and
// a `version` counter that bumps whenever a remote update arrives so the
// containing component can re-read localStorage and update its React state.
export function useLayoutSync(authenticated: boolean) {
  const [version, setVersion] = useState(0)
  const lastSerializedRef = useRef<string>('')
  const debounceRef = useRef<number | undefined>(undefined)
  const inFlightRef = useRef(false)
  const pendingRef = useRef(false)

  // Initial fetch + overlay on top of local cache.
  useEffect(() => {
    if (!authenticated) return
    let cancelled = false
    ;(async () => {
      try {
        const res = await fetch('/api/layout')
        if (!res.ok || cancelled) return
        const body: RemoteLayout = await res.json()
        if (body.data && Object.keys(body.data).length > 0) {
          writeLocalLayout(body.data as LayoutPayload)
          lastSerializedRef.current = JSON.stringify(body.data)
          setVersion(v => v + 1)
        } else {
          // Server is empty; push current local state up as the seed.
          const local = readLocalLayout()
          if (Object.keys(local).length > 0) {
            await fetch('/api/layout', {
              method: 'PUT',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ client_id: LAYOUT_CLIENT_ID, data: local }),
            })
            lastSerializedRef.current = JSON.stringify(local)
          }
        }
      } catch {}
    })()
    return () => { cancelled = true }
  }, [authenticated])

  // Apply a remote layout update (from /ws/events).
  const applyRemote = useCallback((data: LayoutPayload, clientId?: string) => {
    if (clientId && clientId === LAYOUT_CLIENT_ID) return // own echo
    const next = JSON.stringify(data)
    if (next === lastSerializedRef.current) return
    writeLocalLayout(data)
    lastSerializedRef.current = next
    setVersion(v => v + 1)
  }, [])

  // Schedule a debounced PUT of the current localStorage state.
  const pushNow = useCallback(() => {
    if (!authenticated) return
    window.clearTimeout(debounceRef.current)
    debounceRef.current = window.setTimeout(async () => {
      const data = readLocalLayout()
      const serialized = JSON.stringify(data)
      if (serialized === lastSerializedRef.current) return
      if (inFlightRef.current) {
        pendingRef.current = true
        return
      }
      inFlightRef.current = true
      try {
        const res = await fetch('/api/layout', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ client_id: LAYOUT_CLIENT_ID, data }),
        })
        if (res.ok) {
          lastSerializedRef.current = serialized
        }
      } catch {}
      finally {
        inFlightRef.current = false
        if (pendingRef.current) {
          pendingRef.current = false
          pushNow()
        }
      }
    }, 500)
  }, [authenticated])

  // Flush any pending debounced PUT on unload.
  useEffect(() => {
    const onUnload = () => {
      if (!authenticated) return
      const data = readLocalLayout()
      const serialized = JSON.stringify(data)
      if (serialized === lastSerializedRef.current) return
      try {
        const blob = new Blob(
          [JSON.stringify({ client_id: LAYOUT_CLIENT_ID, data })],
          { type: 'application/json' },
        )
        navigator.sendBeacon?.('/api/layout', blob)
      } catch {}
    }
    window.addEventListener('beforeunload', onUnload)
    return () => window.removeEventListener('beforeunload', onUnload)
  }, [authenticated])

  return { version, pushNow, applyRemote }
}
