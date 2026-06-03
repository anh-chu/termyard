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
// session-order) is intentionally NOT here: it describes
// "what's on MY screen" and is per-device. Mirroring viewport across two
// physical screens made them fight over one layout and froze drag/selection.
//
// background-sessions = "this session is parked" and hidden-sessions = "this
// session is hidden from the list". Both are properties of the session,
// meaningful everywhere, so they are safe to share.
const SHARED_KEYS = [
  'guppi:background-sessions',
  'guppi:hidden-sessions',
] as const

type LayoutPayload = Record<string, unknown>

type RemoteLayout = {
  data: Record<string, unknown> | null
  updated_at?: string
  updated_by?: string
}

// ---------------------------------------------------------------------------
// Session-key namespace.
//
// Every session key is GLOBAL and host-qualified everywhere the UI touches it:
// the server's /api/sessions always stamps session.host (PeerMgr is always
// constructed at runtime, and GetAllSessions() sets s.Host = h.ID even for
// THIS node's own sessions), so sessionKey() in useSessions.ts always yields
// "<owner-fp>/<name>". The render filter, the bg/hidden toggles, localStorage,
// and the synced wire payload therefore all speak the SAME host-qualified
// namespace.
//
// HISTORICAL NOTE: this layer used to strip the local fingerprint on the way
// in ("<our-fp>/foo" -> "foo") and re-add it on the way out, on the premise
// that this machine's own sessions were bare-keyed in localStorage. That
// premise was false in multi-host mode — our own sessions are host-prefixed
// too — so the global->local round-trip was NOT the identity. After a reload
// or a phone revisit, applied keys came back bare ("foo") while the render
// filter looked them up host-qualified ("<our-fp>/foo"), so every parked/hidden
// session silently reverted to foreground. The translation is now the identity
// (keys pass through untouched), which both fixes that mismatch and still kills
// the push/echo loop (re-serializing an applied payload yields identical bytes).
// ---------------------------------------------------------------------------

// Identity passthrough. fp is accepted for signature compatibility with the
// old translateShared(fn) call sites but is unused: keys are already global.
function toGlobalKey(key: string, _fp: string): string {
  return key
}

function toLocalKey(key: string, _fp: string): string {
  return key
}

// translateShared previously mapped every shared key through a translation
// fn. Keys are now global on both sides, so this is a structural passthrough
// that simply clones the payload (callers still expect a fresh object).
function translateShared(data: LayoutPayload, _fn: (k: string) => string): LayoutPayload {
  return { ...data }
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
  // Gate outbound writes until the initial GET /api/layout reconciles. A fresh
  // client (e.g. a phone opening the URL the first time) boots with empty
  // localStorage; the Sidebar mount effect immediately calls pushNow(). If the
  // initial fetch hasn't landed yet, that debounced PUT ships an EMPTY payload,
  // and since the server stamps UpdatedAt=now() on every Set() it always wins
  // last-write-wins — wiping the real background-sessions set on the node and
  // fanning the empty state out to every paired peer. Holding pushes until
  // hydration closes that window.
  const hydratedRef = useRef(false)

  const ready = authenticated && !!localFingerprint

  // Initial fetch + overlay on top of local cache.
  useEffect(() => {
    if (!ready) return
    const fp = fpRef.current as string
    let cancelled = false
    hydratedRef.current = false
    ;(async () => {
      try {
        const res = await fetch('/api/layout')
        if (cancelled) return
        if (!res.ok) { hydratedRef.current = true; return }
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
      finally {
        if (!cancelled) hydratedRef.current = true
      }
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
    if (!hydratedRef.current) return // pre-hydration: don't clobber server state
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
      if (!hydratedRef.current) return // pre-hydration: don't clobber server state
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
