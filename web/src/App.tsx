import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { tinykeys } from 'tinykeys'
import { Sidebar } from './components/Sidebar'
import { Terminal } from './components/Terminal'
import { Overview } from './components/Overview'
import { NewSessionModal } from './components/NewSessionModal'
import { PortForwardModal } from './components/PortForwardModal'
import { ScheduleModal } from './components/ScheduleModal'
import { TopBar } from './components/TopBar'
import { TiledView } from './components/TiledView'
import { PaneTree, getLeaves, findLeaf, splitLeaf, insertBesideLeaf, removeLeaf, replaceLeaf, updateRatio, popOut, swapLeaves, movePane } from './lib/paneTree'
import { pruneGroupTree } from './lib/prune'
import { SettingsDrawer } from './components/SettingsDrawer'
import { HelpModal } from './components/HelpModal'
import { QuickSwitcher } from './components/QuickSwitcher'
import { Login } from './components/Login'
import { Setup } from './components/Setup'
import { useSessions, Session, sessionKey, parseSessionKey } from './hooks/useSessions'
import { useHosts } from './hooks/useHosts'
import { useToolEvents } from './hooks/useToolEvents'
import { useActivity } from './hooks/useActivity'
import { useNotifications } from './hooks/useNotifications'
import { useWebSocket } from './hooks/useWebSocket'
import { usePushNotifications } from './hooks/usePushNotifications'
import { usePreferencesProvider, usePreferences, PreferencesContext } from './hooks/usePreferences'
import { useAuth } from './hooks/useAuth'
import { useSessionAttrs } from './hooks/useSessionAttrs'
import { Toasts, Toast } from './components/Toasts'
import { useSelfUpdate, type UpdateStatus } from './hooks/useSelfUpdate'
import { applyTheme } from './theme'
import { sessionSignal } from './lib/sessionState'

type View = 'overview' | 'session' | 'settings' | 'setup'


type LayoutGroup = {
  id: string
  tree: PaneTree
  activeKey: string | null
  name?: string
}

function getViewFromPath(): { view: View; sessionKey: string | null } {
  if (window.location.pathname === '/settings') {
    return { view: 'settings', sessionKey: null }
  }
  if (window.location.pathname === '/setup') {
    return { view: 'setup', sessionKey: null }
  }
  // /session/<host>/<name> or /session/<name> (backward compat)
  const hostMatch = window.location.pathname.match(/^\/session\/([^/]+)\/(.+)$/)
  if (hostMatch) {
    const host = decodeURIComponent(hostMatch[1])
    const name = decodeURIComponent(hostMatch[2])
    return { view: 'session', sessionKey: `${host}/${name}` }
  }
  const match = window.location.pathname.match(/^\/session\/(.+)$/)
  if (match) {
    return { view: 'session', sessionKey: decodeURIComponent(match[1]) }
  }
  return { view: 'overview', sessionKey: null }
}

function AppInner({ onLogout }: { onLogout?: () => void }) {
  const { sessions, loading: sessionsLoading, refresh } = useSessions()
  const { events: allToolEvents, handleEvent: handleToolEvent, getSessionEvents, sessionNeedsAttention, isSessionInActiveTurn, dismissEvent, dismissAll: dismissAllEvents } = useToolEvents()
  const { getSessionActivity, handleActivityEvent } = useActivity()
  const { pushState, subscribe: pushSubscribe, unsubscribe: pushUnsubscribe } = usePushNotifications()
  const { processToolEvent } = useNotifications(pushState === 'subscribed')
  const { hosts, refresh: refreshHosts } = useHosts()
  const [currentView, setCurrentView] = useState<View>(() => {
    const v = getViewFromPath().view
    return v === 'settings' ? 'overview' : v
  })
  const [settingsOpen, setSettingsOpen] = useState(() => getViewFromPath().view === 'settings')
  const [paneTree, setPaneTree] = useState<PaneTree | null>(() => {
    const urlKey = getViewFromPath().sessionKey
    if (!urlKey) return null
    try {
      const stored = localStorage.getItem('termyard:pane-tree')
      if (stored) {
        return JSON.parse(stored) as PaneTree  // always restore full split
      }
    } catch {}
    return popOut(urlKey)
  })
  const [activeKey, setActiveKey] = useState<string | null>(() => {
    const urlKey = getViewFromPath().sessionKey
    if (!urlKey) return null
    try {
      const stored = localStorage.getItem('termyard:pane-tree')
      const storedActiveKey = localStorage.getItem('termyard:active-key')
      if (stored && storedActiveKey) {
        const tree = JSON.parse(stored) as PaneTree
        if (findLeaf(tree, urlKey) && findLeaf(tree, storedActiveKey)) {
          return storedActiveKey
        }
      }
    } catch {}
    return urlKey
  })
  const [singleView, setSingleView] = useState<string | null>(() => {
    const urlKey = getViewFromPath().sessionKey
    if (!urlKey) return null
    try {
      const stored = localStorage.getItem('termyard:pane-tree')
      if (stored) {
        const tree = JSON.parse(stored) as PaneTree
        // If URL session is NOT in the stored split tree, it was a singleView
        if (!findLeaf(tree, urlKey)) return urlKey
      }
    } catch {}
    return null
  })
  const [savedGroups, setSavedGroups] = useState<LayoutGroup[]>(() => {
    try {
      const stored = localStorage.getItem('termyard:saved-groups')
      if (stored) return JSON.parse(stored)
    } catch {}
    return []
  })
  const [activeGroupId, setActiveGroupId] = useState<string>(() => {
    try {
      const stored = localStorage.getItem('termyard:active-group-id')
      if (stored) return stored
    } catch {}
    return Math.random().toString(36).slice(2)
  })
  const [activeGroupName, setActiveGroupName] = useState<string>(() => {
    try { return localStorage.getItem('termyard:active-group-name') || '' } catch { return '' }
  })
  const [groupOrder, setGroupOrder] = useState<string[]>(() => {
    try {
      const stored = localStorage.getItem('termyard:group-order')
      if (stored) {
        const parsed = JSON.parse(stored)
        if (Array.isArray(parsed) && parsed.length > 0) return parsed
      }
    } catch {}
    // Seed from existing group IDs (first run)
    try {
      const activeId = localStorage.getItem('termyard:active-group-id')
      const savedStr = localStorage.getItem('termyard:saved-groups')
      const saved: LayoutGroup[] = savedStr ? JSON.parse(savedStr) : []
      const ids = [activeId, ...saved.map(g => g.id)].filter(Boolean) as string[]
      return Array.from(new Set(ids))
    } catch {}
    return []
  })
  const selectedSession = singleView ?? activeKey
  const hasMultipleHosts = hosts.length > 1
  const localHostId = hosts.find(h => h.local)?.id
  const [serverVersion, setServerVersion] = useState<string | null>(null)
  const [binaryUpdate, setBinaryUpdate] = useState<UpdateStatus | null>(null)
  const loadedVersionRef = useRef<string | null>(null)
  const updateAvailable = loadedVersionRef.current !== null && serverVersion !== null && serverVersion !== loadedVersionRef.current
  const selfUpdate = useSelfUpdate(binaryUpdate)
  const [newSessionModalOpen, setNewSessionModalOpen] = useState(false)
  const terminalContainerRef = useRef<HTMLDivElement>(null)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    try { return localStorage.getItem('termyard:sidebar-collapsed') === 'true' } catch { return false }
  })
  const [sidebarWidth, setSidebarWidth] = useState(() => {
    try {
      const v = parseInt(localStorage.getItem('termyard:sidebar-width') || '', 10)
      if (!Number.isNaN(v)) return Math.min(560, Math.max(200, v))
    } catch {}
    return 288
  })
  const handleSidebarWidth = useCallback((w: number) => {
    setSidebarWidth(w)
    try { localStorage.setItem('termyard:sidebar-width', String(w)) } catch {}
  }, [])
  const [terminalFullscreen, setTerminalFullscreen] = useState(false)
  const [helpOpen, setHelpOpen] = useState(false)
  const [quickSwitcherOpen, setQuickSwitcherOpen] = useState(false)
  const [portForwardsOpen, setPortForwardsOpen] = useState(false)
  const [schedulesOpen, setSchedulesOpen] = useState(false)
  const [mainDragOver, setMainDragOver] = useState<{ type: 'new-session' | 'sidebar'; zone: 'left' | 'right' | 'top' | 'bottom' | 'center' } | null>(null)
  const mainDragOverRef = useRef<{ type: 'new-session' | 'sidebar'; zone: 'left' | 'right' | 'top' | 'bottom' | 'center' } | null>(null)
  const pendingSessionRef = useRef<string | null>(null)
  const splitTargetRef = useRef<{ key: string; direction: 'h' | 'v'; newFirst?: boolean } | null>(null)
  const activeKeyRef = useRef(activeKey)
  activeKeyRef.current = activeKey
  // True while the server is rebuilding sessions after a tmux-server crash.
  // Pruning of missing sessions is suspended until recovery finishes, so a
  // not-yet-rebuilt session is never mistaken for a deliberate kill.
  const [recovering, setRecovering] = useState(false)
  const { prefs } = usePreferences()

  // Shared session attributes (background / hidden) — server-authoritative,
  // mirrored across the mesh. Viewport state (pane-tree, active-key,
  // saved-groups, sidebar-collapsed) stays per-device in localStorage.
  const { sets: sessionAttrs, setAttr: setSessionAttr, refresh: refreshSessionAttrs } = useSessionAttrs(true)

  // Auto-lock: idle detection + optional background accelerator
  const lastActivityRef = useRef<number>(Date.now())
  useEffect(() => {
    if (!onLogout || !prefs.lock_timeout_minutes) return

    const idleMs = prefs.lock_timeout_minutes * 60 * 1000
    const bgMs = prefs.lock_background_faster && prefs.lock_background_minutes
      ? prefs.lock_background_minutes * 60 * 1000
      : idleMs

    // Track user activity
    const onActivity = () => { lastActivityRef.current = Date.now() }
    const events = ['keydown', 'click', 'scroll', 'touchstart', 'mousemove'] as const
    const opts: AddEventListenerOptions = { passive: true, capture: true }
    events.forEach(e => document.addEventListener(e, onActivity, opts))

    // Check idle on an interval
    const checkInterval = setInterval(() => {
      const elapsed = Date.now() - lastActivityRef.current
      const timeout = document.hidden ? bgMs : idleMs
      if (elapsed >= timeout) {
        onLogout()
      }
    }, 30_000)

    // Also check immediately when returning from background
    const onVisibilityChange = () => {
      if (!document.hidden) {
        const elapsed = Date.now() - lastActivityRef.current
        if (elapsed >= bgMs) {
          onLogout()
        }
      }
    }
    document.addEventListener('visibilitychange', onVisibilityChange)

    return () => {
      events.forEach(e => document.removeEventListener(e, onActivity, opts as EventListenerOptions))
      clearInterval(checkInterval)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [onLogout, prefs.lock_timeout_minutes, prefs.lock_background_faster, prefs.lock_background_minutes])

  // Persist sidebar state across reloads. Per-device — NOT synced.
  useEffect(() => {
    localStorage.setItem('termyard:sidebar-collapsed', String(sidebarCollapsed))
  }, [sidebarCollapsed])

  // Persist pane tree across reloads. Per-device — NOT synced.
  useEffect(() => {
    try {
      if (paneTree) {
        localStorage.setItem('termyard:pane-tree', JSON.stringify(paneTree))
        localStorage.setItem('termyard:active-key', activeKey || '')
      } else {
        localStorage.removeItem('termyard:pane-tree')
        localStorage.removeItem('termyard:active-key')
      }
    } catch {}
  }, [paneTree, activeKey])

  // Persist saved groups across reloads. Per-device — NOT synced.
  useEffect(() => {
    try {
      localStorage.setItem('termyard:saved-groups', JSON.stringify(savedGroups))
      localStorage.setItem('termyard:active-group-id', activeGroupId)
      localStorage.setItem('termyard:active-group-name', activeGroupName)
      localStorage.setItem('termyard:group-order', JSON.stringify(groupOrder))
    } catch {}
  }, [savedGroups, activeGroupId, activeGroupName, groupOrder])

  // Sync URL -> state on popstate (back/forward)
  useEffect(() => {
    const onPopState = () => {
      const { view, sessionKey } = getViewFromPath()
      setSettingsOpen(view === 'settings')
      if (view !== 'settings') setCurrentView(view)
      if (sessionKey) {
        setPaneTree(prev => {
          if (prev && findLeaf(prev, sessionKey)) { setActiveKey(sessionKey); setSingleView(null); return prev }
          setSingleView(sessionKey); return prev
        })
      } else {
        setSingleView(null)
        // paneTree and activeKey untouched — split persists in background
      }
    }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

  // Navigate to a session or view (push history)
  // sessKey is either "name" (local) or "host/name" (remote)
  const navigateTo = useCallback((sessKey: string | null, view?: View) => {
    let path: string
    if (view === 'settings') {
      path = '/settings'
    } else if (view === 'setup') {
      path = '/setup'
    } else if (sessKey) {
      const { host, name } = parseSessionKey(sessKey)
      if (host) {
        path = `/session/${encodeURIComponent(host)}/${encodeURIComponent(name)}`
      } else {
        path = `/session/${encodeURIComponent(name)}`
      }
    } else {
      path = '/'
    }
    if (window.location.pathname !== path) {
      window.history.pushState(null, '', path)
    }
    setCurrentView(view || (sessKey ? 'session' : 'overview'))
    if (!sessKey) {
      setSingleView(null)
      // paneTree and activeKey intentionally untouched — split persists
      return
    }
    // sessKey path: kept for safety but selectSession() is preferred
    setSingleView(sessKey)
  }, [])

  const handleDropSession = useCallback((sessKey: string, targetKey: string, edge: 'left'|'right'|'top'|'bottom'|'center') => {
    setSingleView(null)
    const currentActive = activeKeyRef.current
    setPaneTree(prev => {
      // Standalone session: target is the anchor, dragged session always second
      if (prev === null) {
        if (targetKey) {
          const direction: 'h' | 'v' = (edge === 'top' || edge === 'bottom') ? 'v' : 'h'
          const base = popOut(targetKey)
          return splitLeaf(base, targetKey, direction, sessKey)
        }
        return popOut(sessKey)
      }
      // Already in the layout — just focus, don't duplicate
      if (findLeaf(prev, sessKey)) { setActiveKey(sessKey); return prev }
      const key = (targetKey && findLeaf(prev, targetKey)) ? targetKey
        : currentActive !== null && findLeaf(prev, currentActive) ? currentActive
        : getLeaves(prev)[0] ?? null
      if (!key) return popOut(sessKey)
      const direction: 'h' | 'v' = (edge === 'top' || edge === 'bottom') ? 'v' : 'h'
      const newFirst = edge === 'left' || edge === 'top'
      return newFirst
        ? insertBesideLeaf(prev, key, direction, sessKey, true)
        : splitLeaf(prev, key, direction, sessKey)
    })
    setActiveKey(sessKey)
  }, [])

  const closePane = useCallback((sessKey: string) => {
    setPaneTree(prev => {
      if (prev === null) return null
      const newTree = removeLeaf(prev, sessKey)
      if (newTree === null) {
        setActiveKey(null)
        return null
      }
      // If the closed pane was the active one, pick the first leaf
      if (sessKey === activeKeyRef.current) {
        const leaves = getLeaves(newTree)
        setActiveKey(leaves[0] || null)
      }
      return newTree
    })
  }, [])

  // Synchronous removal for a deliberately killed session: drop its leaf from
  // the active tree, any background group, and singleView at once, so the pane
  // disappears immediately instead of on the next session refresh.
  const removeSessionFromLayout = useCallback((sessKey: string) => {
    closePane(sessKey)
    setSingleView(prev => prev === sessKey ? null : prev)
    setSavedGroups(prev =>
      prev.map(group => {
        if (!findLeaf(group.tree, sessKey)) return group
        const keep = new Set(getLeaves(group.tree).filter(k => k !== sessKey))
        const pruned = pruneGroupTree(group.tree, group.activeKey, keep)
        return pruned ? { ...group, tree: pruned.tree, activeKey: pruned.activeKey } : null
      }).filter(Boolean) as LayoutGroup[]
    )
  }, [closePane])

  const popOutPane = useCallback((sessKey: string) => {
    setSingleView(null)
    setPaneTree(popOut(sessKey))
    setActiveKey(sessKey)
    const { host, name } = parseSessionKey(sessKey)
    const path = host
      ? `/session/${encodeURIComponent(host)}/${encodeURIComponent(name)}`
      : `/session/${encodeURIComponent(name)}`
    window.history.pushState(null, '', path)
    setCurrentView('session')
  }, [])

  const killPane = useCallback(async (sessKey: string) => {
    const session = sessionsRef.current.find(s => sessionKey(s) === sessKey)
    if (!session) return
    removeSessionFromLayout(sessKey)
    try {
      await fetch('/api/session/kill', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: session.id, name: session.name, host: session.host || undefined }),
      })
    } catch (err) {
      console.error('Failed to kill session:', err)
    }
  }, [removeSessionFromLayout])

  // Navigate back to overview when the tree becomes empty (but not if singleView is active)
  useEffect(() => {
    if (paneTree === null && !singleView && currentView === 'session') {
      // Remove empty group from order
      const newOrder = groupOrder.filter(id => id !== activeGroupId)
      setGroupOrder(newOrder)
      if (savedGroups.length > 0) {
        // Pick next in stable order
        const nextId = newOrder.find(id => savedGroups.some(g => g.id === id)) ?? savedGroups[0].id
        const next = savedGroups.find(g => g.id === nextId)!
        setSavedGroups(prev => prev.filter(g => g.id !== nextId))
        setPaneTree(next.tree)
        setActiveKey(next.activeKey)
        setActiveGroupId(nextId)
        setActiveGroupName(next.name ?? '')
        const focusKey = next.activeKey ?? getLeaves(next.tree)[0] ?? null
        if (focusKey) {
          const { host, name } = parseSessionKey(focusKey)
          const path = host
            ? `/session/${encodeURIComponent(host)}/${encodeURIComponent(name)}`
            : `/session/${encodeURIComponent(name)}`
          if (window.location.pathname !== path) window.history.pushState(null, '', path)
        }
      } else {
        navigateTo(null)
      }
    }
  }, [paneTree, singleView, currentView, savedGroups, groupOrder, activeGroupId, navigateTo])

  // Dissolve active group to standalone when only 1 session remains
  useEffect(() => {
    if (!paneTree) return
    const leaves = getLeaves(paneTree)
    if (leaves.length !== 1) return
    if (splitTargetRef.current) return // split pending — don't dissolve yet
    const lastLeaf = leaves[0]
    setSingleView(lastLeaf)
    setActiveKey(null)
    setPaneTree(null)
    setGroupOrder(prev => prev.filter(id => id !== activeGroupId))
    const { host, name } = parseSessionKey(lastLeaf)
    const path = host
      ? `/session/${encodeURIComponent(host)}/${encodeURIComponent(name)}`
      : `/session/${encodeURIComponent(name)}`
    if (window.location.pathname !== path) window.history.replaceState(null, '', path)
  }, [paneTree, activeGroupId])

  // Refs for latest values used in keyboard shortcuts (avoids effect churn)

  const sessionsRef = useRef(sessions)
  sessionsRef.current = sessions
  const selectedSessionRef = useRef(selectedSession)
  selectedSessionRef.current = selectedSession
  const savedGroupsRef = useRef(savedGroups)
  savedGroupsRef.current = savedGroups
  const setActiveKeyRef = useRef(setActiveKey)
  setActiveKeyRef.current = setActiveKey
  const switchToGroupRef = useRef<((id: string) => void) | null>(null)

  const openNewSessionModal = useCallback(() => {
    setNewSessionModalOpen(true)
  }, [])

  const openNewSessionPlain = useCallback(() => {
    splitTargetRef.current = null
    setNewSessionModalOpen(true)
  }, [])

  // Global keyboard shortcuts (tinykeys). $mod = Cmd on macOS, Ctrl elsewhere.
  useEffect(() => {
    const cycle = (dir: 1 | -1) => {
      const skeys: string[] = []
      document
        .querySelectorAll('[data-session-key]')
        .forEach(el => skeys.push(el.getAttribute('data-session-key')!))
      if (skeys.length === 0) return
      const current = selectedSessionRef.current
      const idx = current ? skeys.indexOf(current) : -1
      const nextIdx =
        dir === 1
          ? idx >= 0
            ? (idx + 1) % skeys.length
            : 0
          : idx > 0
            ? idx - 1
            : skeys.length - 1
      const targetKey = skeys[nextIdx]
      // If target belongs to a saved group, switch to that group first
      const group = savedGroupsRef.current.find(g => findLeaf(g.tree, targetKey))
      if (group) {
        switchToGroupRef.current?.(group.id)
        setActiveKeyRef.current(targetKey)
        const { host, name } = parseSessionKey(targetKey)
        const path = host
          ? `/session/${encodeURIComponent(host)}/${encodeURIComponent(name)}`
          : `/session/${encodeURIComponent(name)}`
        if (window.location.pathname !== path) window.history.pushState(null, '', path)
      } else {
        selectSessionRef.current?.(targetKey)
      }
    }

    const handler =
      (fn: (e: KeyboardEvent) => void) => (e: KeyboardEvent) => {
        e.preventDefault()
        fn(e)
      }

    // The terminal owns the keyboard. useTerminal() (attachCustomKeyEventHandler)
    // already decides which $mod combos escape xterm and bubble here — that
    // narrow whitelist IS the gate. So we must NOT let tinykeys' default ignore
    // drop events originating from the xterm helper textarea, or whitelisted
    // shortcuts would silently fail while a session is focused. Other form
    // inputs (modals, settings) keep the default ignore behaviour.
    const ignore = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      if (target?.closest?.('.xterm')) return false
      return (
        e.repeat ||
        e.isComposing ||
        (target !== e.currentTarget &&
          !!target?.matches?.('[contenteditable],input,select,textarea'))
      )
    }

    return tinykeys(window, {
      // Help: Cmd/Ctrl + / (Slash). Shift+Slash ('?') handled by same physical key.
      '$mod+Slash': handler(() => setHelpOpen(prev => !prev)),
      '$mod+Shift+Slash': handler(() => setHelpOpen(prev => !prev)),
      // Toggle sidebar: Cmd/Ctrl + \
      '$mod+Backslash': handler(() => setSidebarCollapsed(c => !c)),
      // Settings: Cmd/Ctrl + ,
      '$mod+Comma': handler(() => openSettings()),
      // Split pane: Cmd/Ctrl + Shift + \
      '$mod+Shift+Backslash': handler(() => {
        if (activeKey !== null) {
          splitTargetRef.current = { key: activeKey, direction: 'h' }
          openNewSessionModal()
        }
      }),
      // Quick Switcher: Cmd/Ctrl + Shift + K (K alone collides w/ Firefox search bar)
      '$mod+Shift+k': handler(() => setQuickSwitcherOpen(true)),
      // New session: Cmd/Ctrl + Shift + Enter (N collides w/ browser private window)
      '$mod+Shift+Enter': handler(() => openNewSessionPlain()),
      // Overview: Cmd/Ctrl + Shift + H (Shift+O collides w/ Firefox bookmarks)
      '$mod+Shift+h': handler(() => navigateTo(null)),
      // Cycle sessions: Cmd/Ctrl + Shift + Arrow (Shift+[ / ] switches browser tabs)
      '$mod+Shift+ArrowRight': handler(() => cycle(1)),
      '$mod+Shift+ArrowLeft': handler(() => cycle(-1)),
    }, { ignore })
  }, [navigateTo, activeKey, openNewSessionModal, openNewSessionPlain])

  // Backend notices (silent failures surfaced to the UI as toasts)
  const [toasts, setToasts] = useState<Toast[]>([])
  const toastIdRef = useRef(0)
  const dismissToast = useCallback((id: number) => setToasts(t => t.filter(x => x.id !== id)), [])

  const migrateSessionKey = useCallback((oldKey: string, newKey: string) => {
    if (!oldKey || !newKey || oldKey === newKey) return

    setPaneTree(prev => {
      if (prev === null || !findLeaf(prev, oldKey)) return prev
      return replaceLeaf(prev, oldKey, newKey)
    })
    setActiveKey(prev => (prev === oldKey ? newKey : prev))
    setSingleView(prev => (prev === oldKey ? newKey : prev))
    setSavedGroups(prev => prev.map(group => {
      const hasOldKey = findLeaf(group.tree, oldKey)
      if (!hasOldKey && group.activeKey !== oldKey) return group
      return {
        ...group,
        tree: hasOldKey ? replaceLeaf(group.tree, oldKey, newKey) : group.tree,
        activeKey: group.activeKey === oldKey ? newKey : group.activeKey,
      }
    }))

    const { host: oldHost, name: oldName } = parseSessionKey(oldKey)
    const oldPath = oldHost
      ? `/session/${encodeURIComponent(oldHost)}/${encodeURIComponent(oldName)}`
      : `/session/${encodeURIComponent(oldName)}`
    const { host: newHost, name: newName } = parseSessionKey(newKey)
    const newPath = newHost
      ? `/session/${encodeURIComponent(newHost)}/${encodeURIComponent(newName)}`
      : `/session/${encodeURIComponent(newName)}`
    if (window.location.pathname === oldPath) {
      window.history.replaceState(null, '', newPath)
    }
  }, [])

  // Listen for state events via WebSocket
  const onEvent = useCallback((evt: any) => {
    if (evt.type === 'notice') {
      const d = evt.data || {}
      setToasts(t => [
        ...t,
        {
          id: ++toastIdRef.current,
          severity: d.severity === 'error' || d.severity === 'warn' ? d.severity : 'info',
          source: d.source || 'server',
          message: d.message || '',
          session: evt.session || undefined,
        },
      ].slice(-4))
      return
    }
    if (evt.type === 'welcome') {
      const v = evt.version || null
      if (!loadedVersionRef.current) {
        loadedVersionRef.current = v
      }
      setServerVersion(v)
      return
    }
    if (evt.type === 'tool-event') {
      handleToolEvent(evt)
      processToolEvent(evt)
      return
    }
    if (evt.type === 'activity') {
      handleActivityEvent(evt.snapshots || [])
      return

    }
    if (evt.type === 'session-renamed') {
      const oldName = evt.session || ''
      const newName = evt.data?.new_name || ''
      if (!oldName || !newName) return
      const host = evt.host || ''
      const oldKey = host ? `${host}/${oldName}` : oldName
      const newKey = host ? `${host}/${newName}` : newName
      migrateSessionKey(oldKey, newKey)
      refresh()
      return
    }
    if (evt.type === 'recovery-started') {
      setRecovering(true)
      return
    }
    if (evt.type === 'recovery-finished') {
      setRecovering(false)
      refresh()
      return
    }
    if (['session-added', 'session-removed', 'sessions-changed'].includes(evt.type)) {
      refresh()
    }
    if (['peer-connected', 'peer-disconnected'].includes(evt.type)) {
      refresh()
      refreshHosts()
    }
    if (evt.type === 'update-status') {
      setBinaryUpdate(evt)
      return
    }
    if (evt.type === 'session-attrs-updated') {
      refreshSessionAttrs()
    }
  }, [refresh, refreshHosts, handleToolEvent, processToolEvent, handleActivityEvent, refreshSessionAttrs, migrateSessionKey])

  const { connected } = useWebSocket('/ws/events', onEvent)

  // Prune leaves whose session is gone from the live list. While the server is
  // alive the list is authoritative, so a missing session is a genuine kill and
  // its pane is removed at once. Recovery (full-server rebuild) is the only time
  // a live session is transiently absent; pruning is suspended then.
  useEffect(() => {
    if (sessionsLoading || sessions.length === 0 || recovering) return
    const validKeys = new Set(sessions.map(s => sessionKey(s)))
    if (pendingSessionRef.current) validKeys.add(pendingSessionRef.current)

    if (paneTree) {
      const toRemove = getLeaves(paneTree).filter(k => !validKeys.has(k))
      if (toRemove.length > 0) {
        setPaneTree(prev => {
          if (prev === null) return null
          let tree: PaneTree | null = prev
          for (const key of toRemove) {
            if (tree === null) break
            tree = removeLeaf(tree, key)
          }
          if (tree && activeKeyRef.current && toRemove.includes(activeKeyRef.current)) {
            setActiveKey(getLeaves(tree)[0] || null)
          }
          return tree
        })
      }
    }

    setSingleView(prev => (prev && !validKeys.has(prev)) ? null : prev)

    setSavedGroups(prev =>
      prev.map(group => {
        const pruned = pruneGroupTree(group.tree, group.activeKey, validKeys)
        if (!pruned) return null
        if (pruned.tree === group.tree && pruned.activeKey === group.activeKey) return group
        return { ...group, tree: pruned.tree, activeKey: pruned.activeKey }
      }).filter(Boolean) as LayoutGroup[]
    )
  }, [sessions, sessionsLoading, paneTree, savedGroups, singleView, recovering]) // eslint-disable-line react-hooks/exhaustive-deps

  // Release the pending-session guard once the freshly created session shows
  // up in state (remote creates arrive via a delayed peer broadcast).
  useEffect(() => {
    const pending = pendingSessionRef.current
    if (!pending) return
    if (sessions.some(s => sessionKey(s) === pending)) pendingSessionRef.current = null
  }, [sessions])

  // (saved-group pruning is handled by the unified grace-based effect above)



  const selectSessionRef = useRef<((sk: string) => void) | null>(null)

  const refocusTerminal = useCallback(() => {
    requestAnimationFrame(() => {
      const textarea = terminalContainerRef.current?.querySelector('textarea.xterm-helper-textarea') as HTMLTextAreaElement | null
      textarea?.focus()
    })
  }, [])

  const selectSession = useCallback((sk: string) => {
    const { host, name } = parseSessionKey(sk)
    const path = host
      ? `/session/${encodeURIComponent(host)}/${encodeURIComponent(name)}`
      : `/session/${encodeURIComponent(name)}`
    if (window.location.pathname !== path) window.history.pushState(null, '', path)
    setCurrentView('session')
    if (paneTree && findLeaf(paneTree, sk)) {
      setSingleView(null)
      setActiveKey(sk)
    } else {
      setSingleView(sk)
    }
    // Refocus even when activeKey didn't change — Terminal auto-focus
    // on inactive panes may have stolen visual focus from the intended one.
    setTimeout(refocusTerminal, 150)
  }, [paneTree, refocusTerminal])
  selectSessionRef.current = selectSession

  const handleSessionSelect = (session: Session) => {
    selectSession(sessionKey(session))
  }

  const handlePairSessions = useCallback((draggedKey: string, targetKey: string) => {
    setSingleView(null)
    const inCurrentTree = paneTree && (findLeaf(paneTree, draggedKey) || findLeaf(paneTree, targetKey))
    if (!inCurrentTree) {
      // Neither session is in the active group — create a new background group
      const newId = Math.random().toString(36).slice(2)
      const newTree: PaneTree = { type: 'split', direction: 'h', ratio: 0.5,
        first: { type: 'leaf', sessionKey: targetKey },
        second: { type: 'leaf', sessionKey: draggedKey } }
      // Save current group if it has a tree
      if (paneTree) {
        setSavedGroups(prev => [...prev, { id: activeGroupId, tree: paneTree, activeKey, name: activeGroupName || undefined }])
      }
      setPaneTree(newTree)
      setActiveKey(draggedKey)
      setActiveGroupId(newId)
      setActiveGroupName('')
      setGroupOrder(prev => {
        // ensure current activeGroupId is in order, then append new group
        const withCurrent = prev.includes(activeGroupId) ? prev : [...prev, activeGroupId]
        return [...withCurrent, newId]
      })
      setSingleView(null)
      const { host, name } = parseSessionKey(draggedKey)
      const path = host ? `/session/${encodeURIComponent(host)}/${encodeURIComponent(name)}` : `/session/${encodeURIComponent(name)}`
      if (window.location.pathname !== path) window.history.pushState(null, '', path)
      setCurrentView('session')
      return
    }
    // Existing behavior: add to current group's tree
    setPaneTree(prev => {
      if (prev && findLeaf(prev, draggedKey) && findLeaf(prev, targetKey)) return prev
      if (prev && findLeaf(prev, targetKey)) return splitLeaf(prev, targetKey, 'h', draggedKey)
      if (prev && findLeaf(prev, draggedKey)) return splitLeaf(prev, draggedKey, 'h', targetKey)
      return { type: 'split', direction: 'h', ratio: 0.5,
        first: { type: 'leaf', sessionKey: targetKey },
        second: { type: 'leaf', sessionKey: draggedKey } }
    })
    setActiveKey(draggedKey)
    const { host, name } = parseSessionKey(draggedKey)
    const path = host ? `/session/${encodeURIComponent(host)}/${encodeURIComponent(name)}` : `/session/${encodeURIComponent(name)}`
    if (window.location.pathname !== path) window.history.pushState(null, '', path)
    setCurrentView('session')
  }, [paneTree, activeKey, activeGroupId, groupOrder])

  const switchToGroup = useCallback((groupId: string, focusKey?: string) => {
    // If re-selecting the already-active group (e.g. after navigating to a standalone session),
    // just clear singleView to restore the pane tree view.
    if (groupId === activeGroupId && paneTree) {
      setSingleView(null)
      setCurrentView('session')
      const targetKey = focusKey ?? activeKey
      if (targetKey) {
        const { host, name } = parseSessionKey(targetKey)
        const path = host
          ? `/session/${encodeURIComponent(host)}/${encodeURIComponent(name)}`
          : `/session/${encodeURIComponent(name)}`
        if (window.location.pathname !== path) window.history.pushState(null, '', path)
      }
      setTimeout(refocusTerminal, 150)
      return
    }
    const targetGroup = savedGroups.find(g => g.id === groupId)
    if (!targetGroup) return
    // Save current active group if it has a tree
    if (paneTree) {
      setSavedGroups(prev => [
        ...prev.filter(g => g.id !== groupId),
        { id: activeGroupId, tree: paneTree, activeKey, name: activeGroupName || undefined }
      ])
    } else {
      setSavedGroups(prev => prev.filter(g => g.id !== groupId))
    }
    const targetKey = (focusKey && findLeaf(targetGroup.tree, focusKey))
      ? focusKey
      : (targetGroup.activeKey ?? getLeaves(targetGroup.tree)[0] ?? null)
    setPaneTree(targetGroup.tree)
    setActiveKey(targetKey)
    setActiveGroupId(groupId)
    setActiveGroupName(targetGroup.name ?? '')
    setSingleView(null)
    setCurrentView('session')
    // Navigate URL to the target leaf
    if (targetKey) {
      const { host, name } = parseSessionKey(targetKey)
      const path = host
        ? `/session/${encodeURIComponent(host)}/${encodeURIComponent(name)}`
        : `/session/${encodeURIComponent(name)}`
      if (window.location.pathname !== path) window.history.pushState(null, '', path)
    }
    setTimeout(refocusTerminal, 150)
  }, [savedGroups, activeGroupId, activeGroupName, paneTree, activeKey, refocusTerminal])
  switchToGroupRef.current = switchToGroup

  const renameGroup = useCallback((groupId: string, name: string) => {
    if (groupId === activeGroupId) {
      setActiveGroupName(name)
    } else {
      setSavedGroups(prev => prev.map(g => g.id === groupId ? { ...g, name: name || undefined } : g))
    }
  }, [activeGroupId])

  // Safety-net refocus when activeKey changes via paths that don't call
  // selectSession (e.g. onActivate from clicking inside TiledView).
  useEffect(() => {
    if (currentView === 'session' && paneTree && activeKey) {
      setTimeout(refocusTerminal, 150)
    }
  }, [activeKey, currentView, paneTree, refocusTerminal])

  const jumpToSession = useCallback(async (sessKey: string, windowIndex?: number, pane?: string) => {
    selectSession(sessKey)
    if (windowIndex !== undefined) {
      const { host, name } = parseSessionKey(sessKey)
      try {
        await fetch('/api/session/select-window', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ host: host || undefined, session: name, window: windowIndex, pane: pane || undefined }),
        })
      } catch (err) {
        console.error('Failed to select window:', err)
      }
    }
    setTimeout(() => refocusTerminal(), 200)
  }, [selectSession, refocusTerminal])

  const prevPathRef = useRef<string>('/')
  const openSettings = useCallback(() => {
    if (window.location.pathname !== '/settings') {
      prevPathRef.current = window.location.pathname
      window.history.pushState(null, '', '/settings')
    }
    setSettingsOpen(true)
  }, [])
  const closeSettings = useCallback(() => {
    setSettingsOpen(false)
    if (window.location.pathname === '/settings') {
      window.history.pushState(null, '', prevPathRef.current || '/')
    }
  }, [])

  const handleCreateSession = useCallback(async (
    name: string,
    path: string,
    command: string,
    hostId?: string,
    worktreeBranch?: string,
    agentType?: string,
    splitTarget?: { key: string; direction: 'h' | 'v'; newFirst?: boolean },
  ): Promise<string | null> => {
    // For worktree sessions keep the modal open until we confirm success.
    if (!worktreeBranch) setNewSessionModalOpen(false)
    try {
      const res = await fetch('/api/session/new', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, path, command, host: hostId || undefined, agent_type: agentType || undefined, worktree_branch: worktreeBranch || undefined }),
      })
      if (!res.ok) {
        if (worktreeBranch) {
          const msg = await res.text().catch(() => 'Failed to create worktree')
          return msg
        }
        return null
      }
      if (worktreeBranch) setNewSessionModalOpen(false)
      {
        const payload = await res.json().catch(() => null)
        const resolvedName = payload?.name || name
        const sessKey = hostId ? `${hostId}/${resolvedName}` : resolvedName
        pendingSessionRef.current = sessKey
        // Remote creates round-trip through the peer, so the session is not in
        // hub state right after refresh(). Keep the pending key protected from
        // the prune effect until it materializes (cleared by the effect below);
        // fall back to a timeout so a failed create can't pin it forever.
        const pendingKey = sessKey
        window.setTimeout(() => {
          if (pendingSessionRef.current === pendingKey) pendingSessionRef.current = null
        }, 15000)
        // Direct parameter takes priority over ref (avoids race when drag fires twice)
        const target = splitTarget ?? splitTargetRef.current
        splitTargetRef.current = null
        if (target) {
          setPaneTree(prev => {
            if (prev === null) return popOut(sessKey)
            if (findLeaf(prev, target.key)) {
              return target.newFirst
                ? insertBesideLeaf(prev, target.key, target.direction, sessKey, true)
                : splitLeaf(prev, target.key, target.direction, sessKey)
            }
            return prev
          })
          setActiveKey(sessKey)
          await refresh()
          setTimeout(() => refocusTerminal(), 300)
        } else {
          selectSession(sessKey)
          await refresh()
          setTimeout(() => refocusTerminal(), 300)
        }
      }
    } catch (err) {
      console.error('Failed to create session:', err)
      pendingSessionRef.current = null
    }
    return null
  }, [selectSession, refresh, refocusTerminal])

  const handleDropNewSession = useCallback((targetKey: string, edge: 'left'|'right'|'top'|'bottom'|'center') => {
    let key = targetKey || activeKey

    // Dropping onto a singleView session (standalone, not in any group):
    // save current group to background and start a new group from singleView
    if (!targetKey && singleView) {
      key = singleView
      const newGroupId = Math.random().toString(36).slice(2)
      if (paneTree) {
        setSavedGroups(prev => [...prev, { id: activeGroupId, tree: paneTree, activeKey, name: activeGroupName || undefined }])
      }
      setGroupOrder(prev => {
        const withCurrent = paneTree && !prev.includes(activeGroupId) ? [...prev, activeGroupId] : prev
        return [...withCurrent, newGroupId]
      })
      setPaneTree(popOut(singleView))
      setActiveKey(singleView)
      setActiveGroupId(newGroupId)
      setActiveGroupName('')
      setSingleView(null)
    }

    let splitTarget: { key: string; direction: 'h' | 'v'; newFirst?: boolean } | undefined
    if (key) {
      const direction: 'h' | 'v' = (edge === 'top' || edge === 'bottom') ? 'v' : 'h'
      const newFirst = edge === 'left' || edge === 'top'
      splitTarget = { key, direction, newFirst }
      // Also set ref for dissolve-effect guard; handleCreateSession prefers direct param
      splitTargetRef.current = splitTarget
    }
    const { host } = key ? parseSessionKey(key) : { host: undefined }
    // Pass splitTarget directly — avoids ref race when event fires on both pane and container
    handleCreateSession('shell', '~', '', host || undefined, undefined, undefined, splitTarget)
  }, [singleView, activeKey, activeGroupId, paneTree, handleCreateSession])

  const toggleFullscreen = useCallback(() => {
    setTerminalFullscreen(f => !f)
  }, [])

  // Keep the browser title stable unless user attention is needed.
  useEffect(() => {
    const needsAttention = allToolEvents.some(
      evt => evt.status === 'waiting' || evt.status === 'error' || evt.status === 'stuck',
    )
    document.title = needsAttention ? 'Termyard - Attention needed' : 'Termyard'
  }, [allToolEvents])

  // Exit fullscreen when navigating away from terminal
  useEffect(() => {
    if (currentView !== 'session') {
      setTerminalFullscreen(false)
    }
  }, [currentView])

  const showingTerminal = currentView === 'session' && !!selectedSession

  const glance = useMemo(() => {
    let parked = 0
    let working = 0
    let waiting = 0
    for (const session of sessions) {
      const key = sessionKey(session)
      const signal = sessionSignal(session, getSessionEvents(key), getSessionActivity(key), isSessionInActiveTurn(key))
      if (signal.state === 'needs_you') waiting++
      else if (signal.state === 'working') working++
      else parked++
    }
    return { parked, working, waiting }
  }, [sessions, getSessionEvents, getSessionActivity, isSessionInActiveTurn, allToolEvents])

  return (
    <div className="flex flex-col h-full w-full bg-background text-foreground relative">
      <Toasts toasts={toasts} onDismiss={dismissToast} />
      {helpOpen && <HelpModal onClose={() => setHelpOpen(false)} />}
      {portForwardsOpen && (
        <PortForwardModal
          hostId={selectedSession ? parseSessionKey(selectedSession).host || undefined : undefined}
          onClose={() => setPortForwardsOpen(false)}
        />
      )}
      {schedulesOpen && (
        <ScheduleModal onClose={() => setSchedulesOpen(false)} />
      )}
      {quickSwitcherOpen && (
        <QuickSwitcher
          sessions={sessions}
          waitingEvents={allToolEvents}
          onSelect={(sessionName, windowIndex) => {
            selectSession(sessionName)
            setQuickSwitcherOpen(false)
            if (windowIndex !== undefined) {
              const { host, name } = parseSessionKey(sessionName)
              fetch('/api/session/select-window', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ host: host || undefined, session: name, window: windowIndex }),
              }).catch(err => console.error('Failed to select window:', err))
            }
          }}
          onOverview={() => {
            navigateTo(null)
            setQuickSwitcherOpen(false)
          }}
          onCreateSession={() => {
            openNewSessionModal()
            setQuickSwitcherOpen(false)
          }}
          onClose={() => setQuickSwitcherOpen(false)}
        />
      )}
      {newSessionModalOpen && (
        <NewSessionModal
          hosts={hosts}
          sessions={sessions}
          onCreateSession={handleCreateSession}
          onClose={() => setNewSessionModalOpen(false)}
        />
      )}
      {/* TopBar - full width */}
      {(!terminalFullscreen || !prefs.fullscreen_hide_alerts) && (
        <TopBar
          currentView={currentView}
          settingsActive={settingsOpen}
          selfUpdateAvailable={selfUpdate.updateVisible}
          updateVersion={selfUpdate.status?.latest_version}
          onApplyUpdate={selfUpdate.apply}
          updateApplying={selfUpdate.applying}
          onDismissUpdate={selfUpdate.dismiss}
          onOverview={() => navigateTo(null)}
          onSettings={openSettings}
          onHelp={() => setHelpOpen(true)}
          onNewSession={openNewSessionModal}
          onPortForwards={() => setPortForwardsOpen(true)}
          onSchedules={() => setSchedulesOpen(true)}
          events={allToolEvents}
          connected={connected}
          onJumpToSession={jumpToSession}
          onDismiss={dismissEvent}
          onDismissAll={dismissAllEvents}
          panesCount={paneTree ? getLeaves(paneTree).length : 0}
          onSplitPane={() => {
            if (activeKey !== null) {
              splitTargetRef.current = { key: activeKey, direction: 'h' }
            }
            openNewSessionModal()
          }}
          glance={glance}
        />
      )}
      {/* Middle: Sidebar + Content */}
      <div className="flex-1 flex overflow-hidden">
        {!terminalFullscreen && (
          <Sidebar
            sessions={sessions}
            selectedSession={selectedSession}
            collapsed={sidebarCollapsed}
            selfUpdateAvailable={selfUpdate.status?.update_available ?? false}
            collapseMode={(prefs.sidebar.collapse_mode || 'small') as 'small' | 'hidden'}
            width={sidebarWidth}
            onWidthChange={handleSidebarWidth}
            hasMultipleHosts={hasMultipleHosts}
            localHostId={localHostId}
            hosts={hosts}
            onSessionSelect={handleSessionSelect}
            getSessionEvents={getSessionEvents}
            sessionNeedsAttention={sessionNeedsAttention}
            isSessionInActiveTurn={isSessionInActiveTurn}
            getSessionActivity={getSessionActivity}
            agentCount={allToolEvents.filter(e => e.auto_detected || e.status === 'waiting' || e.status === 'error' || e.status === 'stuck').length}
            glance={glance}
            onToggleCollapse={() => setSidebarCollapsed(c => !c)}
            layoutGroups={groupOrder
              .map(id => {
                if (id === activeGroupId && paneTree)
                  return { id, leaves: getLeaves(paneTree), isActive: currentView === 'session' && singleView === null, activeKey, name: activeGroupName || undefined }
                const g = savedGroups.find(g => g.id === id)
                if (g) return { id, leaves: getLeaves(g.tree), isActive: false, activeKey: g.activeKey, name: g.name }
                return null
              })
              .filter((g): g is { id: string; leaves: string[]; isActive: boolean; activeKey: string | null; name: string | undefined } => g !== null)
            }
            onSwitchGroup={switchToGroup}
            onRenameGroup={renameGroup}
            onPairSessions={handlePairSessions}
            onRemoveFromSplit={closePane}
            onSessionKilled={removeSessionFromLayout}
            sessionAttrs={sessionAttrs}
            setSessionAttr={setSessionAttr}
          />
        )}
        <div
          className="flex-1 flex flex-col overflow-hidden relative"
          onDragOver={(e) => {
            const dt = e.dataTransfer
            const getZone = (): 'left'|'right'|'top'|'bottom'|'center' => {
              const rect = e.currentTarget.getBoundingClientRect()
              const x = e.clientX - rect.left
              const y = e.clientY - rect.top
              const w = rect.width
              const h = rect.height
              if (x < w * 0.25) return 'left'
              if (x > w * 0.75) return 'right'
              if (y < h * 0.25) return 'top'
              if (y > h * 0.75) return 'bottom'
              return 'center'
            }
            if (dt.types.includes('application/x-termyard-new-session')) {
              e.preventDefault()
              e.dataTransfer.dropEffect = 'copy'
              const val = { type: 'new-session' as const, zone: getZone() }
              mainDragOverRef.current = val
              setMainDragOver(val)
              return
            }
            if (dt.types.includes('text/plain') && !dt.types.includes('application/x-termyard-pane')) {
              e.preventDefault()
              const val = { type: 'sidebar' as const, zone: getZone() }
              mainDragOverRef.current = val
              setMainDragOver(val)
            }
          }}
          onDragLeave={(e) => {
            if (!e.currentTarget.contains(e.relatedTarget as Node)) {
              mainDragOverRef.current = null
              setMainDragOver(null)
            }
          }}
          onDrop={(e) => {
            e.preventDefault()
            const zone = mainDragOverRef.current?.zone ?? 'center'
            mainDragOverRef.current = null
            setMainDragOver(null)
            if (e.dataTransfer.types.includes('application/x-termyard-new-session')) {
              handleDropNewSession('', zone)
              return
            }
            const sessKey = e.dataTransfer.getData('text/plain')
            if (sessKey && !e.dataTransfer.types.includes('application/x-termyard-pane')) {
              handleDropSession(sessKey, singleView ?? '', zone)
            }
          }}
        >
          {mainDragOver && (
            <div className="absolute inset-0 z-50 pointer-events-none">
              {/* Edge strip */}
              <div
                className="absolute bg-primary"
                style={{
                  ...(mainDragOver.zone === 'left' && { left: 0, top: 0, bottom: 0, width: 1 }),
                  ...(mainDragOver.zone === 'right' && { right: 0, top: 0, bottom: 0, width: 1 }),
                  ...(mainDragOver.zone === 'top' && { top: 0, left: 0, right: 0, height: 1 }),
                  ...(mainDragOver.zone === 'bottom' && { bottom: 0, left: 0, right: 0, height: 1 }),
                }}
              />
              {mainDragOver.zone === 'center' ? (
                <div className="absolute inset-0 bg-primary/10 border-2 border-dashed border-primary rounded-lg flex items-center justify-center">
                  <span className="text-sm font-medium text-primary">+ Split</span>
                </div>
              ) : (
                <div
                  className="absolute bg-primary/10"
                  style={{
                    ...(mainDragOver.zone === 'left' && { left: 0, top: 0, bottom: 0, width: '50%' }),
                    ...(mainDragOver.zone === 'right' && { right: 0, top: 0, bottom: 0, width: '50%' }),
                    ...(mainDragOver.zone === 'top' && { top: 0, left: 0, right: 0, height: '50%' }),
                    ...(mainDragOver.zone === 'bottom' && { bottom: 0, left: 0, right: 0, height: '50%' }),
                  }}
                />
              )}
            </div>
          )}
          {currentView === 'setup' ? (
            <Setup onComplete={() => navigateTo(null)} />
          ) : currentView === 'session' && singleView ? (
            <div ref={terminalContainerRef} className="flex-1 flex flex-col overflow-hidden">
              <Terminal
                sessionName={parseSessionKey(singleView).name}
                hostId={parseSessionKey(singleView).host || undefined}
                fullscreen={terminalFullscreen}
                onToggleFullscreen={toggleFullscreen}
              />
            </div>
          ) : currentView === 'session' && paneTree ? (
            <TiledView
              tree={paneTree}
              activeKey={activeKey}
              onActivate={(key) => { setActiveKey(key); refocusTerminal() }}
              onClose={closePane}
              onKill={killPane}
              onPopOut={popOutPane}
              onSplit={(key, direction) => {
                splitTargetRef.current = { key, direction }
                openNewSessionModal()
              }}
              onRatioChange={(path, ratio) => {
                setPaneTree(prev => {
                  if (prev === null) return null
                  return updateRatio(prev, path, ratio)
                })
              }}
              fullscreen={terminalFullscreen}
              onToggleFullscreen={toggleFullscreen}
              terminalContainerRef={terminalContainerRef}
              onDropSession={handleDropSession}
              onDropNewSession={handleDropNewSession}
              onSwapPanes={(a, b) => setPaneTree(prev => prev ? swapLeaves(prev, a, b) : prev)}
              onMovePanes={(sourceKey, targetKey, edge) =>
                setPaneTree(prev => prev ? movePane(prev, sourceKey, targetKey, edge) : prev)
              }
            />
          ) : (
            <Overview
              sessions={sessions}
              hosts={hosts}
              hiddenSet={sessionAttrs.hidden}
              backgroundSet={sessionAttrs.background}
              onSessionSelect={handleSessionSelect}
              getSessionEvents={getSessionEvents}
              getSessionActivity={getSessionActivity}
              isSessionInActiveTurn={isSessionInActiveTurn}
              onJumpToSession={jumpToSession}
              onDismissAlert={dismissEvent}
            />
          )}
          <SettingsDrawer
            open={settingsOpen}
            onClose={closeSettings}
            pushState={pushState}
            onPushSubscribe={pushSubscribe}
            onPushUnsubscribe={pushUnsubscribe}
            onLogout={onLogout}
            version={serverVersion}
            updateAvailable={updateAvailable}
            binaryUpdate={selfUpdate.status}
            onApplyUpdate={selfUpdate.apply}
            updateApplying={selfUpdate.applying}
            updateRestartMode={selfUpdate.restartMode}
            updateError={selfUpdate.error}
            updateChecking={selfUpdate.checking}
            onCheckUpdate={selfUpdate.checkNow}
          />
        </div>
      </div>
    </div>
  )
}

export default function App() {
  const prefsProvider = usePreferencesProvider()
  const { loading, authRequired, needsSetup, authenticated, error: authError, setup, login, logout } = useAuth()
  const [showOnboarding, setShowOnboarding] = useState(false)

  useEffect(() => {
    const syncViewport = () => {
      const viewport = window.visualViewport
      const height = viewport?.height ?? window.innerHeight
      const width = viewport?.width ?? window.innerWidth
      document.documentElement.style.setProperty('--app-height', `${Math.round(height)}px`)
      document.documentElement.style.setProperty('--app-width', `${Math.round(width)}px`)
    }

    syncViewport()
    window.addEventListener('resize', syncViewport)
    window.visualViewport?.addEventListener('resize', syncViewport)
    window.visualViewport?.addEventListener('scroll', syncViewport)

    return () => {
      window.removeEventListener('resize', syncViewport)
      window.visualViewport?.removeEventListener('resize', syncViewport)
      window.visualViewport?.removeEventListener('scroll', syncViewport)
    }
  }, [])

  // Re-fetch preferences after login (initial fetch may have gotten 401)
  useEffect(() => {
    if (authenticated) {
      prefsProvider.refetch()
    }
  }, [authenticated]) // eslint-disable-line react-hooks/exhaustive-deps

  // Apply last-used theme immediately (before auth) so login page is themed
  useEffect(() => {
    try {
      const cached = localStorage.getItem('termyard:theme')
      const cachedCustom = localStorage.getItem('termyard:custom-theme')
      if (cached) {
        applyTheme(cached, cachedCustom ? JSON.parse(cachedCustom) : undefined)
      }
    } catch {}
  }, [])

  // Apply theme when preferences load or theme/customizations change, and cache for login page
  useEffect(() => {
    if (prefsProvider.loaded) {
      applyTheme(prefsProvider.prefs.theme, prefsProvider.prefs.custom_theme)
      try {
        localStorage.setItem('termyard:theme', prefsProvider.prefs.theme)
        localStorage.setItem('termyard:custom-theme', JSON.stringify(prefsProvider.prefs.custom_theme || {}))
      } catch {}
    }
  }, [prefsProvider.loaded, prefsProvider.prefs.theme, prefsProvider.prefs.custom_theme])


  useEffect(() => {
    const displayFont = prefsProvider.prefs.display_font || 'Space Mono'
    document.documentElement.style.setProperty('--font-display', `"${displayFont}", "JetBrains Mono", monospace`)
    document.documentElement.setAttribute('data-texture', prefsProvider.prefs.texture_enabled === false ? 'off' : 'on')
  }, [prefsProvider.prefs.display_font, prefsProvider.prefs.texture_enabled])

  if (loading) {
    return <div className="flex items-center justify-center h-full w-full bg-background" />
  }

  if (authRequired && needsSetup) {
    const handleSetup = async (password: string) => {
      const ok = await setup(password)
      if (ok) setShowOnboarding(true)
      return ok
    }
    return <Login mode="setup" error={authError} onSubmit={handleSetup} />
  }

  if (authRequired && !authenticated) {
    return <Login mode="login" error={authError} onSubmit={login} />
  }

  if (authenticated && showOnboarding) {
    return (
      <PreferencesContext.Provider value={prefsProvider}>
        <Setup fullPage onComplete={() => {
          setShowOnboarding(false)
          try { localStorage.setItem('termyard:setup-seen', 'true') } catch {}
        }} />
      </PreferencesContext.Provider>
    )
  }

  return (
    <PreferencesContext.Provider value={prefsProvider}>
      <AppInner onLogout={authRequired ? logout : undefined} />
    </PreferencesContext.Provider>
  )
}
