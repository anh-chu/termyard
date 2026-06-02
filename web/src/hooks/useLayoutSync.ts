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

// Keys mirrored to the server-side store and fanned out to paired peers.
//
// IMPORTANT: this channel carries SHARED SESSION ATTRIBUTES only — facts about
// a session that are true on every machine. Viewport/layout state (pane-tree,
// active-key, saved-groups, group-order, sidebar-collapsed, collapsed-groups,
// session-order, hidden-sessions) is intentionally NOT here: it describes
// "what's on MY screen" and is per-device. Mirroring viewport across two
// physical screens made them fight over one layout and froze drag/selection.
//
// background-sessions = "this session is parked". That's a property of the
// session, meaningful everywhere, so it is safe to share.
const SHARED_KEYS = [
  'guppi:background-sessions',
] as const

type LayoutPayload = Record<string, unknown>

type RemoteLayout = {
  data: Record<string, unknown> | null
  updated_at?: string
  updated_by?: string
}

// ---------------------------------------------------------------------------
// Global <-> local session-key namespace translation.
//
// Session keys are MACHINE-RELATIVE in localStorage: a session owned by THIS
// machine is the bare name ("foo"); a session owned by a peer is host-prefixed
// ("<peer-fp>/foo"). Two machines therefore disagree on what "foo" means.
//
// To share attributes across machines we serialize in a GLOBAL namespace where
// EVERY session is "<owner-fp>/<name>". The local machine's own sessions get
// our fingerprint prefixed on the way out and stripped on the way in. Peer
// keys are already global and pass through untouched. The round-trip
// (global -> local -> global) is the identity, which is what kills the
// last-write-wins push/echo loop: re-serializing an applied remote payload
// yields the exact bytes we received, so pushNow() short-circuits.
// ---------------------------------------------------------------------------

// Mirrors parseSessionKey()/sessionKey() in useSessions.ts. The first '/'
// separates host from name; names may themselves contain '/'.
function splitKey(key: string): { host: string; name: string } {
  const idx = key.indexOf('/')
  if (idx === -1) return { host: '', name: key }
  return { host: key.substring(0, idx), name: key.substring(idx + 1) }
}

function toGlobalKey(localKey: string, fp: string): string {
  if (!localKey) return localKey
  const { host } = splitKey(localKey)
  // Bare key = a session owned by this machine -> qualify with our fp.
  return host === '' ? `${fp}/${localKey}` : localKey
}

function toLocalKey(globalKey: string, fp: string): string {
  if (!globalKey) return globalKey
  const { host, name } = splitKey(globalKey)
  // Our own sessions become bare; peer sessions stay host-qualified.
  return host === fp ? name : globalKey
}

// Translate every shared key (all flat string arrays of session keys) through
// fn. Keeping the shared set to flat arrays keeps this trivial — no recursive
// pane-tree / saved-group walking.
function translateShared(data: LayoutPayload, fn: (k: string) => string): LayoutPayload {
  const out: LayoutPayload = { ...data }
  for (const k of SHARED_KEYS) {
    if (Array.isArray(out[k])) {
      out[k] = (out[k] as string[]).map(fn)
    }
  }
  return out
}

// Deterministic JSON with recursively sorted object keys. Two machines build
// the payload object in different key orders, so a plain JSON.stringify diff
// would never converge — this canonical form is used ONLY for the
// loop-suppression comparison (lastSerializedRef), not for the wire payload.
function canonical(value: unknown): string {
  return JSON.stringify(value, function replacer(_key, val) {
    if (val && typeof val === 'object' && !Array.isArray(val)) {
      const sorted: Record<string, unknown> = {}
      for (const k of Object.keys(val as Record<string, unknown>).sort()) {
        sorted[k] = (val as Record<string, unknown>)[k]
      }
      return sorted
    }
    return val
  })
}

function readShared(): LayoutPayload {
  const out: LayoutPayload = {}
  for (const k of SHARED_KEYS) {
    try {
      const raw = localStorage.getItem(k)
      if (raw == null) continue
      try { out[k] = JSON.parse(raw) }
      catch { out[k] = raw }
    } catch {}
  }
  return out
}

function writeShared(data: LayoutPayload | null) {
  if (!data) return
  for (const k of SHARED_KEYS) {
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

// useLayoutSync wires the shared-session-attribute localStorage keys to the
// server-side store. It returns a `pushNow` function that schedules a debounced
// PUT, and a `version` counter that bumps whenever a remote update arrives so
// the containing component can re-read localStorage and update React state.
//
// `localFingerprint` is this machine's identity fingerprint. It is required
// for cross-machine sharing (local<->global key translation); until it is
// known, sync is held off so we never persist/transmit half-translated keys.
export function useLayoutSync(authenticated: boolean, localFingerprint: string | null) {
  const [version, setVersion] = useState(0)
  const lastSerializedRef = useRef<string>('')
  const debounceRef = useRef<number | undefined>(undefined)
  const inFlightRef = useRef(false)
  const pendingRef = useRef(false)
  const fpRef = useRef<string | null>(localFingerprint)
  fpRef.current = localFingerprint

  const ready = authenticated && !!localFingerprint

  // Initial fetch + overlay on top of local cache.
  useEffect(() => {
    if (!ready) return
    const fp = fpRef.current as string
    let cancelled = false
    ;(async () => {
      try {
        const res = await fetch('/api/layout')
        if (!res.ok || cancelled) return
        const body: RemoteLayout = await res.json()
        if (body.data && Object.keys(body.data).length > 0) {
          // Remote payload is global -> translate to local before storing.
          const global = body.data as LayoutPayload
          writeShared(translateShared(global, k => toLocalKey(k, fp)))
          lastSerializedRef.current = canonical(global)
          setVersion(v => v + 1)
        } else {
          // Server is empty; push current local state up as the seed (global).
          const local = readShared()
          if (Object.keys(local).length > 0) {
            const global = translateShared(local, k => toGlobalKey(k, fp))
            await fetch('/api/layout', {
              method: 'PUT',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ client_id: LAYOUT_CLIENT_ID, data: global }),
            })
            lastSerializedRef.current = canonical(global)
          }
        }
      } catch {}
    })()
    return () => { cancelled = true }
  }, [ready])

  // Apply a remote update (from /ws/events). `data` is in the GLOBAL namespace;
  // translate to local keys before writing localStorage.
  const applyRemote = useCallback((data: LayoutPayload, clientId?: string) => {
    if (clientId && clientId === LAYOUT_CLIENT_ID) return // own echo (same tab)
    const fp = fpRef.current
    if (!fp) return // can't translate yet — drop; initial fetch will reconcile
    const next = canonical(data)
    if (next === lastSerializedRef.current) return
    writeShared(translateShared(data, k => toLocalKey(k, fp)))
    lastSerializedRef.current = next
    setVersion(v => v + 1)
  }, [])

  // Schedule a debounced PUT of the current shared state (translated to the
  // global namespace).
  const pushNow = useCallback(() => {
    if (!authenticated) return
    const fp = fpRef.current
    if (!fp) return
    window.clearTimeout(debounceRef.current)
    debounceRef.current = window.setTimeout(async () => {
      const global = translateShared(readShared(), k => toGlobalKey(k, fp))
      const serialized = canonical(global)
      // Short-circuit: re-pushing an applied remote payload yields identical
      // canonical bytes (global round-trips), so the echo loop terminates.
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
          body: JSON.stringify({ client_id: LAYOUT_CLIENT_ID, data: global }),
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
      const fp = fpRef.current
      if (!fp) return
      const global = translateShared(readShared(), k => toGlobalKey(k, fp))
      const serialized = canonical(global)
      if (serialized === lastSerializedRef.current) return
      try {
        const blob = new Blob(
          [JSON.stringify({ client_id: LAYOUT_CLIENT_ID, data: global })],
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
