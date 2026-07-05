import { useRef, useCallback, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { ClipboardAddon, type IClipboardProvider, type ClipboardSelectionType } from '@xterm/addon-clipboard'
import { usePreferences } from './usePreferences'
import { getXtermTheme } from '../theme'
// xterm CSS is imported in main.tsx (before index.css) so our overrides win.

// Monotonically increasing ID to track which connection is "current"
let nextConnId = 0

// macOS overlay scrollbars reserve 0px, which xterm's viewport turns into a
// 15px fallback (`offsetWidth - scrollArea.offsetWidth || 15`). FitAddon then
// subtracts that 15px, clipping ~20px of content on the right. We hide the
// scrollbar in CSS and force the internal scrollBarWidth back to 0 so the grid
// uses the full pane width. Must run after term.open() and before each fit().
type XtermWithCore = Terminal & {
  _core?: { viewport?: { scrollBarWidth?: number } }
}
function neutralizeXtermScrollbarFallback(term: Terminal): void {
  const viewport = (term as XtermWithCore)._core?.viewport
  if (viewport && typeof viewport.scrollBarWidth === 'number') {
    viewport.scrollBarWidth = 0
  }
}

// Pending clipboard text to write on the next user interaction.
// Clipboard API requires user activation; OSC 52 and selection events arrive
// asynchronously, so we stash the text and flush it on the next mousedown/keydown.
let pendingClipboard: string | null = null

// Synchronous fallback using execCommand('copy') — works inside and outside
// user-gesture context in most browsers because the textarea selection counts
// as a copy-eligible action.
function execCommandCopy(text: string): boolean {
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.position = 'fixed'
  ta.style.left = '-9999px'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch { /* ignored */ }
  document.body.removeChild(ta)
  return ok
}

// Try to write to clipboard immediately; on failure, use execCommand fallback;
// if that also fails, stash for deferred write on next user gesture.
function copyToClipboard(text: string): Promise<void> {
  if (navigator.clipboard) {
    return navigator.clipboard.writeText(text).catch(() => {
      if (!execCommandCopy(text)) {
        pendingClipboard = text
      }
    })
  }
  if (!execCommandCopy(text)) {
    pendingClipboard = text
  }
  return Promise.resolve()
}

// Flush any stashed clipboard text — call from a user gesture handler.
function flushPendingClipboard(): void {
  if (pendingClipboard !== null) {
    const text = pendingClipboard
    pendingClipboard = null
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text).catch(() => {
        if (!execCommandCopy(text)) {
          pendingClipboard = text
        }
      })
    } else if (!execCommandCopy(text)) {
      pendingClipboard = text
    }
  }
}

// Request clipboard-write permission so future writes don't require user activation.
// Chromium grants this persistently once allowed; Firefox/Safari ignore it (harmless).
function requestClipboardPermission(): void {
  navigator.permissions?.query({ name: 'clipboard-write' as PermissionName }).catch(() => {})
}

// Custom clipboard provider that uses the fallback copy for OSC 52 writes
const clipboardProvider: IClipboardProvider = {
  readText(selection: ClipboardSelectionType): Promise<string> {
    if (selection !== 'c') return Promise.resolve('')
    return navigator.clipboard?.readText?.() ?? Promise.resolve('')
  },
  writeText(selection: ClipboardSelectionType, text: string): Promise<void> {
    if (selection !== 'c') return Promise.resolve()
    return copyToClipboard(text)
  },
}

const MAX_PASTED_IMAGE_BYTES = 10 * 1024 * 1024

function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  const chunkSize = 0x8000
  for (let i = 0; i < bytes.length; i += chunkSize) {
    const chunk = bytes.subarray(i, i + chunkSize)
    binary += String.fromCharCode(...chunk)
  }
  return btoa(binary)
}

async function sendPastedImage(ws: WebSocket, file: File, fallbackType: string): Promise<void> {
  if (file.size > MAX_PASTED_IMAGE_BYTES) {
    console.warn(`Pasted image exceeds ${MAX_PASTED_IMAGE_BYTES} byte limit`)
    return
  }

  const buffer = await file.arrayBuffer()
  ws.send(JSON.stringify({
    type: 'paste-image',
    data: bytesToBase64(new Uint8Array(buffer)),
    mime: file.type || fallbackType,
    filename: file.name,
  }))
}

export function useTerminal(sessionName: string, hostId?: string) {
  const termRef = useRef<Terminal | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const containerRef = useRef<HTMLElement | null>(null)
  const listenerCleanupRef = useRef<(() => void) | null>(null)
  const reconnectTimer = useRef<number | undefined>(undefined)
  const heartbeatTimer = useRef<number | undefined>(undefined)
  const watchdogTimer = useRef<number | undefined>(undefined)
  const activeConnId = useRef(0)
  const [termConnected, setTermConnected] = useState(false)
  const [ctrlModifierActive, setCtrlModifierActive] = useState(false)
  const ctrlModifierRef = useRef(false)
  const [altModifierActive, setAltModifierActive] = useState(false)
  const altModifierRef = useRef(false)
  const suppressedInputRef = useRef<string | null>(null)
  const [selectionMenu, setSelectionMenu] = useState<{ x: number; y: number; text: string } | null>(null)
  const { prefs } = usePreferences()
  const sendRawBytes = useCallback((bytes: Uint8Array) => {
    const currentWs = wsRef.current
    if (currentWs && currentWs.readyState === WebSocket.OPEN) {
      currentWs.send(bytes)
    }
  }, [])

  const sendText = useCallback((text: string) => {
    if (!text) return
    sendRawBytes(new TextEncoder().encode(text))
  }, [sendRawBytes])

  const sendImage = useCallback((file: File, fallbackType: string) => {
    const currentWs = wsRef.current
    if (!currentWs || currentWs.readyState !== WebSocket.OPEN) return
    sendPastedImage(currentWs, file, fallbackType).catch((err) => {
      console.error('Failed to send pasted image:', err)
    })
  }, [])

  const clearCtrlModifier = useCallback(() => {
    ctrlModifierRef.current = false
    setCtrlModifierActive(false)
  }, [])

  const toggleCtrlModifier = useCallback(() => {
    ctrlModifierRef.current = !ctrlModifierRef.current
    setCtrlModifierActive(ctrlModifierRef.current)
  }, [])

  const clearAltModifier = useCallback(() => {
    altModifierRef.current = false
    setAltModifierActive(false)
  }, [])

  const toggleAltModifier = useCallback(() => {
    altModifierRef.current = !altModifierRef.current
    setAltModifierActive(altModifierRef.current)
  }, [])

  const cleanupWs = useCallback(() => {
    if (wsRef.current) {
      // Nullify handlers BEFORE closing to prevent ghost reconnects
      wsRef.current.onclose = null
      wsRef.current.onerror = null
      wsRef.current.onmessage = null
      wsRef.current.close()
      wsRef.current = null
    }
  }, [])

  const connect = useCallback((container: HTMLElement) => {
    // Invalidate any previous connection's reconnect attempts
    const connId = ++nextConnId
    activeConnId.current = connId

    // Clean up any existing terminal and WS
    if (reconnectTimer.current) {
      clearTimeout(reconnectTimer.current)
      reconnectTimer.current = undefined
    }
    if (heartbeatTimer.current) {
      clearInterval(heartbeatTimer.current)
      heartbeatTimer.current = undefined
    }
    if (watchdogTimer.current) {
      clearTimeout(watchdogTimer.current)
      watchdogTimer.current = undefined
    }
    cleanupWs()
    if (termRef.current) {
      listenerCleanupRef.current?.()
      listenerCleanupRef.current = null
      termRef.current.dispose()
    }

    containerRef.current = container

    const xtermTheme = getXtermTheme(prefs.theme)
    const fontFamily = `'${prefs.terminal.font_family}', 'JetBrains Mono', 'Fira Code', Menlo, Monaco, 'Inconsolata LGC Nerd Font Mono', 'DejaVu Sans Mono Symbols', monospace`

    // Create terminal with theme from preferences
    const term = new Terminal({
      theme: xtermTheme,
      fontSize: prefs.terminal.font_size,
      fontFamily,
      cursorBlink: true,
      scrollback: prefs.terminal.scrollback,
      allowProposedApi: true,
      rightClickSelectsWord: true,
      macOptionClickForcesSelection: true,
      // Send Option/Alt as Meta (ESC-prefix) so Alt+key bindings reach the
      // terminal app (tmux/vim/readline) on macOS too. No-op on Linux (already
      // ESC-prefix by default).
      macOptionIsMeta: true,
    })

    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.loadAddon(new WebLinksAddon())
    term.loadAddon(new ClipboardAddon(undefined, clipboardProvider))

    termRef.current = term
    fitAddonRef.current = fitAddon
    // DEBUG: expose for diagnostics (remove after debugging)
    ;(window as any).__term = term

    term.open(container)
    neutralizeXtermScrollbarFallback(term)

    const helperTextarea = container.querySelector('textarea.xterm-helper-textarea') as HTMLTextAreaElement | null

    // Request clipboard-write permission early so OSC 52 writes may work directly
    requestClipboardPermission()

    // Cmd/Ctrl+C: copy selection to clipboard if text is selected,
    // otherwise let it pass through as SIGINT.
    // Cmd/Ctrl+B: force-send tmux prefix so browser shortcuts can't steal it.
    // Also flush any pending clipboard on every keydown (user gesture context).
    term.attachCustomKeyEventHandler((e) => {
      if (e.type === 'keydown') {
        flushPendingClipboard()
      }
      if (
        e.type === 'keydown' &&
        ctrlModifierRef.current &&
        !e.metaKey &&
        !e.ctrlKey &&
        !e.altKey &&
        e.key.length === 1
      ) {
        const key = e.key.toUpperCase()
        if (key >= 'A' && key <= 'Z') {
          suppressedInputRef.current = e.key
          sendRawBytes(new Uint8Array([key.charCodeAt(0) - 64]))
          clearCtrlModifier()
          return false
        }
      }
      if (
        e.type === 'keydown' &&
        altModifierRef.current &&
        !e.metaKey &&
        !e.ctrlKey &&
        !e.altKey &&
        e.key.length === 1
      ) {
        suppressedInputRef.current = e.key
        sendRawBytes(new Uint8Array([0x1b, ...new TextEncoder().encode(e.key)]))
        clearAltModifier()
        return false
      }
      // Don't let xterm process global app shortcuts — let them bubble to the
      // window-level tinykeys handler in App.tsx / Terminal fullscreen handler.
      // Keep this list in sync with the bindings registered there.
      if (e.type === 'keydown' && (e.metaKey || e.ctrlKey)) {
        const key = e.key.toLowerCase()
        if (!e.shiftKey) {
          // Settings (,), sidebar (\), help (/ or ?)
          if (key === ',' || key === '\\' || key === '/' || key === '?') {
            return false
          }
        } else {
          // Shift family: help (/ or ?), split (\), quick switcher (k),
          // new session (Enter), overview (h), fullscreen (f), cycle sessions (arrows)
          if (
            key === '/' ||
            key === '?' ||
            key === '\\' ||
            key === 'k' ||
            key === 'enter' ||
            key === 'h' ||
            key === 'f' ||
            e.key === 'ArrowLeft' ||
            e.key === 'ArrowRight'
          ) {
            return false
          }
        }
      }
      if ((e.metaKey || e.ctrlKey) && e.key === 'c' && e.type === 'keydown') {
        const selection = term.getSelection()
        if (selection) {
          // Direct user gesture — clipboard write succeeds without stashing
          navigator.clipboard?.writeText(selection)
          term.clearSelection()
          return false // prevent sending to terminal
        }
        // Explicitly send SIGINT (0x03) — iPad/tablets may not translate
        // Ctrl+C correctly, causing it to act as Enter instead
        sendRawBytes(new Uint8Array([0x03]))
        return false
      }
      if ((e.metaKey || e.ctrlKey) && e.key === 'b' && e.type === 'keydown') {
        sendRawBytes(new Uint8Array([0x02]))
        return false
      }
      return true
    })

    // Auto-copy to clipboard on selection (like iTerm2 / most terminal emulators)
    term.onSelectionChange(() => {
      const selection = term.getSelection()
      if (selection) {
        copyToClipboard(selection)
      }
    })

    // Flush deferred clipboard writes on mouse interaction (capture phase
    // to intercept before xterm.js can stopPropagation)
    const onMouseDown = (e: MouseEvent) => {
      flushPendingClipboard()
      // Right-click on an existing xterm selection: keep it local (our menu),
      // don't let xterm forward the button-2 mouse report to tmux. Capture
      // phase runs before xterm's own .xterm-screen listener.
      if (e.button === 2 && term.getSelection()) {
        e.preventDefault()
        e.stopPropagation()
        e.stopImmediatePropagation()
      }
    }
    const onKeyDown = () => flushPendingClipboard()
    const onWindowKeyDownCapture = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.key.toLowerCase() !== 'b') return

      const active = document.activeElement
      const terminalFocused = !!active && (container.contains(active) || active === container)
      if (!terminalFocused) return

      e.preventDefault()
      e.stopPropagation()
      sendRawBytes(new Uint8Array([0x02]))
    }
    const onPaste = (e: ClipboardEvent) => {
      const items = Array.from(e.clipboardData?.items ?? [])
      const imageItem = items.find(item => item.type.startsWith('image/'))
      if (!imageItem) return

      const file = imageItem.getAsFile()
      const currentWs = wsRef.current
      if (!file || !currentWs || currentWs.readyState !== WebSocket.OPEN) return

      e.preventDefault()

      sendPastedImage(currentWs, file, imageItem.type)
        .catch((err) => {
          console.error('Failed to read pasted image:', err)
        })
    }
    const onContextMenu = (e: MouseEvent) => {
      e.preventDefault()
      // With tmux mouse mode on, plain drag goes to tmux; only a Shift+drag
      // leaves an xterm-owned selection here. No selection -> pass through.
      const sel = term.getSelection()
      if (sel) setSelectionMenu({ x: e.clientX, y: e.clientY, text: sel })
    }

    container.addEventListener('mousedown', onMouseDown, true)
    container.addEventListener('keydown', onKeyDown, true)
    window.addEventListener('keydown', onWindowKeyDownCapture, true)
    helperTextarea?.addEventListener('paste', onPaste)

    // Suppress browser's native context menu so right-click passes through to tmux
    container.addEventListener('contextmenu', onContextMenu)
    listenerCleanupRef.current = () => {
      container.removeEventListener('mousedown', onMouseDown, true)
      container.removeEventListener('keydown', onKeyDown, true)
      window.removeEventListener('keydown', onWindowKeyDownCapture, true)
      helperTextarea?.removeEventListener('paste', onPaste)
      container.removeEventListener('contextmenu', onContextMenu)
    }

    // Fit terminal to container — retry a few times to handle layout settling
    const doFit = () => {
      try {
        if (container.clientWidth > 0 && container.clientHeight > 0) {
          neutralizeXtermScrollbarFallback(term)
          fitAddon.fit()
        }
      } catch {}
    }
    doFit()
    requestAnimationFrame(doFit)
    setTimeout(doFit, 100)
    setTimeout(doFit, 300)

    // The terminal web fonts (Space Mono / JetBrains Mono) load asynchronously
    // via Google Fonts with display=swap. xterm measures the cell width against
    // the fallback font first, computes too many columns, then the real (wider)
    // font swaps in and content overflows and is clipped on the right. Once the
    // font is actually loaded, force xterm to re-measure the cell size and
    // re-fit so the column count matches the rendered glyph width.
    const remeasureAndFit = () => {
      try {
        ;(term as unknown as { _core?: { _charSizeService?: { measure?: () => void } } })
          ._core?._charSizeService?.measure?.()
      } catch {}
      doFit()
    }
    if (typeof document !== 'undefined' && (document as Document).fonts) {
      const fontSpec = `${prefs.terminal.font_size}px ${fontFamily}`
      document.fonts.load(fontSpec).then(remeasureAndFit).catch(() => {})
      document.fonts.load(`bold ${fontSpec}`).catch(() => {})
      document.fonts.load(`${prefs.terminal.font_size}px 'Inconsolata LGC Nerd Font Mono'`).then(remeasureAndFit).catch(() => {})
      document.fonts.load(`${prefs.terminal.font_size}px 'DejaVu Sans Mono Symbols'`).catch(() => {})
      document.fonts.ready.then(remeasureAndFit).catch(() => {})
    }

    // Get initial dimensions
    const cols = term.cols || 80
    const rows = term.rows || 24

    // Connect WebSocket for this session
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const hostParam = hostId ? `&host=${encodeURIComponent(hostId)}` : ''
    const wsUrl = `${protocol}//${window.location.host}/ws/session?name=${encodeURIComponent(sessionName)}&cols=${cols}&rows=${rows}${hostParam}`
    const ws = new WebSocket(wsUrl)
    ws.binaryType = 'arraybuffer'
    wsRef.current = ws
    let sawFirstByte = false
    let msgCount = 0
    let totalBytes = 0
    let lastSummary = 0
    const tConnect = performance.now()

    // Liveness watchdog. A half-open TCP (laptop sleep, wifi handoff, NAT
    // idle timeout) leaves ws.readyState === OPEN: output silently stops and
    // keystrokes vanish with no error and no onclose, so the disconnect
    // overlay never shows. We app-level ping every 10s and force-close if no
    // traffic arrives for ~25s, which triggers the normal reconnect path.
    const HEARTBEAT_MS = 10000
    const WATCHDOG_MS = 25000
    const armWatchdog = () => {
      if (watchdogTimer.current) clearTimeout(watchdogTimer.current)
      watchdogTimer.current = window.setTimeout(() => {
        if (activeConnId.current !== connId) return
        // Stale socket — drop it so onclose schedules a reconnect.
        try { ws.close() } catch { /* ignored */ }
      }, WATCHDOG_MS)
    }

    ws.onopen = () => {
      // Stale connection — a newer connect() was called
      if (activeConnId.current !== connId) {
        ws.close()
        return
      }
      setTermConnected(true)
      armWatchdog()
      if (heartbeatTimer.current) clearInterval(heartbeatTimer.current)
      heartbeatTimer.current = window.setInterval(() => {
        if (ws.readyState === WebSocket.OPEN) {
          try { ws.send(JSON.stringify({ type: 'ping' })) } catch { /* ignored */ }
        }
      }, HEARTBEAT_MS)
    }

    ws.onmessage = (evt) => {
      // Any inbound traffic proves the link is alive.
      armWatchdog()
      if (evt.data instanceof ArrayBuffer) {
        msgCount++
        totalBytes += evt.data.byteLength
        const head = new Uint8Array(evt.data.slice(0, 48))
        const sample = Array.from(head).map(b => (b >= 32 && b < 127) ? String.fromCharCode(b) : '\\x' + b.toString(16).padStart(2, '0')).join('')
        if (!sawFirstByte) {
          sawFirstByte = true
        }
        const before = term.buffer.active.length
        term.write(new Uint8Array(evt.data))
        const now = Date.now()
        if (now - lastSummary > 500) {
          lastSummary = now
        }
      } else {
        // Text frames are control messages (pong). Don't echo to the terminal.
        if (typeof evt.data === 'string') {
          try {
            const ctrl = JSON.parse(evt.data)
            if (ctrl && ctrl.type === 'pong') return
          } catch { /* not JSON — fall through and write raw */ }
        }
        term.write(evt.data as string)
      }
    }

    ws.onclose = (evt) => {
      const detail = `connId=${connId} code=${evt.code} reason=${evt.reason || ''} clean=${evt.wasClean} msgs=${msgCount} hidden=${document.hidden} +${(performance.now() - tConnect).toFixed(0)}ms ${sawFirstByte ? 'painted' : 'never-painted'}`
      // Only handle if this is still the active connection
      if (activeConnId.current !== connId) {
        return
      }
      if (heartbeatTimer.current) {
        clearInterval(heartbeatTimer.current)
        heartbeatTimer.current = undefined
      }
      if (watchdogTimer.current) {
        clearTimeout(watchdogTimer.current)
        watchdogTimer.current = undefined
      }
      // Don't flash the disconnect overlay if the page is just hidden
      if (!document.hidden) {
        setTermConnected(false)
      }
      // If hidden (e.g. iPad app switch), defer reconnect until page is visible again
      if (document.hidden) {
        const onVisible = () => {
          if (activeConnId.current !== connId) return
          document.removeEventListener('visibilitychange', onVisible)
          window.removeEventListener('pageshow', onVisible)
          if (containerRef.current && activeConnId.current === connId) {
            connect(containerRef.current)
          }
        }
        document.addEventListener('visibilitychange', onVisible)
        window.addEventListener('pageshow', onVisible)
      } else if (containerRef.current) {
        reconnectTimer.current = window.setTimeout(() => {
          if (containerRef.current && activeConnId.current === connId) {
            connect(containerRef.current)
          }
        }, 2000)
      }
    }

    ws.onerror = (err) => {
      console.error(`Terminal WS error: session ${sessionName}`, err)
    }

    // Forward keystrokes as binary messages
    term.onData((data) => {
      if (suppressedInputRef.current !== null && data === suppressedInputRef.current) {
        suppressedInputRef.current = null
        return
      }
      suppressedInputRef.current = null
      if (ws.readyState === WebSocket.OPEN) {
        const encoder = new TextEncoder()
        ws.send(encoder.encode(data))
      }
    })

    // Send resize events as JSON text messages
    term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    })
  }, [sessionName, hostId, cleanupWs, prefs.theme, prefs.terminal.font_size, prefs.terminal.font_family, prefs.terminal.scrollback, sendRawBytes])

  const disconnect = useCallback(() => {
    // Invalidate any active connection
    activeConnId.current = ++nextConnId
    if (reconnectTimer.current) {
      clearTimeout(reconnectTimer.current)
      reconnectTimer.current = undefined
    }
    if (heartbeatTimer.current) {
      clearInterval(heartbeatTimer.current)
      heartbeatTimer.current = undefined
    }
    if (watchdogTimer.current) {
      clearTimeout(watchdogTimer.current)
      watchdogTimer.current = undefined
    }
    cleanupWs()
    if (termRef.current) {
      listenerCleanupRef.current?.()
      listenerCleanupRef.current = null
      termRef.current.dispose()
      termRef.current = null
    }
    containerRef.current = null
  }, [cleanupWs])

  const fit = useCallback(() => {
    const term = termRef.current
    if (fitAddonRef.current && containerRef.current && term) {
      try {
        if (containerRef.current.clientWidth > 0 && containerRef.current.clientHeight > 0) {
          neutralizeXtermScrollbarFallback(term)
          fitAddonRef.current.fit()
          // ponytail: repaint from buffer clears ghost rows left by the
          // CSS-stretched canvas during a debounced/no-net-change resize
          // (tmux only redraws on a net SIGWINCH, so xterm must self-clear).
          term.refresh(0, term.rows - 1)
        }
      } catch {}
    }
  }, [])

  const focus = useCallback(() => {
    termRef.current?.focus()
  }, [])

  // Rebind xterm to the container's current window after the DOM node moves
  // between documents (PiP pop-out / restore). xterm 5.5 caches the window in
  // CoreBrowserService at open() time and drives the renderer's
  // requestAnimationFrame from it; calling open() again updates that window
  // when ownerDocument changed, so the renderer paints in the new window
  // (Firefox stops painting without this; Chrome tolerates the stale binding).
  const rebind = useCallback(() => {
    const term = termRef.current
    const container = containerRef.current
    if (!term || !container) return
    try { term.open(container) } catch { /* ignored */ }
    fit()
    try { term.refresh(0, term.rows - 1) } catch { /* ignored */ }
  }, [fit])

  return {
    termRef,
    connect,
    disconnect,
    fit,
    rebind,
    focus,
    termConnected,
    sendRawBytes,
    sendText,
    sendImage,
    ctrlModifierActive,
    toggleCtrlModifier,
    clearCtrlModifier,
    altModifierActive,
    toggleAltModifier,
    clearAltModifier,
    selectionMenu,
    setSelectionMenu,
  }
}
