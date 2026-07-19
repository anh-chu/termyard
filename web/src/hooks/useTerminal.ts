import { useRef, useCallback, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { ClipboardAddon, type IClipboardProvider, type ClipboardSelectionType } from '@xterm/addon-clipboard'
import { WebglAddon } from '@xterm/addon-webgl'
import { ImageAddon } from '@xterm/addon-image'
import { UnicodeGraphemesAddon } from '@xterm/addon-unicode-graphemes'
import { usePreferences } from './usePreferences'
import { getXtermTheme } from '../theme'
import { PredictiveEcho } from '../lib/predictive-echo'
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

// xterm.js includes trailing pad spaces up to the selection column on rows
// shorter than the terminal width (e.g. triple-click line select), and hard
// line breaks baked into the scrollback during a pane reflow can
// leave short trailing-space runs at wrap points. Strip trailing whitespace
// from each line before it hits the clipboard.
export function normalizeSelection(text: string): string {
  return text.replace(/[ \t]+$/gm, '')
}

// Try to write to clipboard immediately; on failure, use execCommand fallback;
// if that also fails, stash for deferred write on next user gesture.
function copyToClipboard(text: string): Promise<void> {
  text = normalizeSelection(text)
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

const MAX_PASTED_FILE_BYTES = 10 * 1024 * 1024

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
  if (file.size > MAX_PASTED_FILE_BYTES) {
    console.warn(`Pasted image exceeds ${MAX_PASTED_FILE_BYTES} byte limit`)
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

// Shared scroll-preserving fit used by both doFit (initial layout) and
// the exported fit() callback (ResizeObserver / pane drag). Before fit it
// snapshots the distance from the bottom; after fit it restores the
// viewport position with a synchronous pass + double-rAF (catches xterm.js
// async viewport updates) + DOM failsafes at 50ms / 200ms for at-bottom.
function fitPreservingScroll(
  term: Terminal,
  fitAddon: FitAddon,
  container: HTMLElement,
  opts?: { refreshAfter?: boolean },
): void {
  if (container.clientWidth <= 0 || container.clientHeight <= 0) return

  const buf = term.buffer.active
  const distFromBottom = Math.max(0, buf.baseY - buf.viewportY)
  const wasAtBottom = distFromBottom === 0

  neutralizeXtermScrollbarFallback(term)
  fitAddon.fit()

  if (opts?.refreshAfter) {
    try { term.refresh(0, term.rows - 1) } catch { /* renderer dispose race */ }
  }

  const restoreScroll = () => {
    try {
      if (wasAtBottom) { term.scrollToBottom(); return }
      const after = term.buffer.active
      if (distFromBottom > after.baseY) { term.scrollToBottom(); return }
      const target = after.baseY - distFromBottom
      const delta = target - after.viewportY
      if (delta !== 0) term.scrollLines(delta)
    } catch { /* renderer dispose race */ }
  }
  const forceDOM = () => {
    if (!wasAtBottom) return
    const vp = container.querySelector('.xterm-viewport') as HTMLElement | null
    if (vp && vp.scrollTop + vp.clientHeight < vp.scrollHeight - 5) {
      vp.scrollTop = vp.scrollHeight
    }
  }

  restoreScroll()
  requestAnimationFrame(() => {
    restoreScroll()
    requestAnimationFrame(() => {
      restoreScroll()
      forceDOM()
    })
  })
  setTimeout(() => { restoreScroll(); forceDOM() }, 50)
  setTimeout(() => { restoreScroll(); forceDOM() }, 200)
}

export function useTerminal(sessionName: string, hostId?: string, backend?: string) {
  const termRef = useRef<Terminal | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const webglAddonRef = useRef<WebglAddon | null>(null)
  const imageAddonRef = useRef<ImageAddon | null>(null)
  const graphemesAddonRef = useRef<UnicodeGraphemesAddon | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const containerRef = useRef<HTMLElement | null>(null)
  const listenerCleanupRef = useRef<(() => void) | null>(null)
  const reconnectTimer = useRef<number | undefined>(undefined)
  const heartbeatTimer = useRef<number | undefined>(undefined)
  const watchdogTimer = useRef<number | undefined>(undefined)
  const activeConnId = useRef(0)
  const telemetryIntervalRef = useRef<number | undefined>(undefined)
  const [termConnected, setTermConnected] = useState(false)
  const [ctrlModifierActive, setCtrlModifierActive] = useState(false)
  const ctrlModifierRef = useRef(false)
  const [altModifierActive, setAltModifierActive] = useState(false)
  const altModifierRef = useRef(false)
  const suppressedInputRef = useRef<string | null>(null)
  const [selectionMenu, setSelectionMenu] = useState<{ x: number; y: number; text: string } | null>(null)
  const { prefs } = usePreferences()
  const rendererRef = useRef(prefs.terminal.renderer)
  const graphemesPrefRef = useRef(prefs.terminal.unicode_graphemes)
  const predictiveEchoPrefRef = useRef(prefs.terminal.predictive_echo)
  const graphemesLoadedRef = useRef(false)
  const predictiveEchoRef = useRef<PredictiveEcho | null>(null)
  // Keep refs in sync with latest prefs so stable callbacks never use stale values.
  rendererRef.current = prefs.terminal.renderer
  graphemesPrefRef.current = prefs.terminal.unicode_graphemes
  predictiveEchoPrefRef.current = prefs.terminal.predictive_echo
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
    if (telemetryIntervalRef.current) {
      clearInterval(telemetryIntervalRef.current)
      telemetryIntervalRef.current = undefined
    }
    cleanupWs()
    if (webglAddonRef.current) {
      webglAddonRef.current.dispose()
      webglAddonRef.current = null
    }
    if (imageAddonRef.current) {
      imageAddonRef.current.dispose()
      imageAddonRef.current = null
    }
    if (graphemesAddonRef.current) {
      graphemesAddonRef.current.dispose()
      graphemesAddonRef.current = null
      graphemesLoadedRef.current = false
    }
    if (predictiveEchoRef.current) {
      predictiveEchoRef.current.dispose()
      predictiveEchoRef.current = null
    }
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
      // terminal app (vim/readline) on macOS too. No-op on Linux (already
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

    // Conditionally load WebGL addon for GPU-accelerated rendering
    if (rendererRef.current === 'webgl') {
      try {
        const webglAddon = new WebglAddon()
        webglAddon.onContextLoss(() => {
          webglAddon.dispose()
          webglAddonRef.current = null
        })
        term.loadAddon(webglAddon)
        webglAddonRef.current = webglAddon
      } catch (e) {
        console.warn('WebGL addon failed to load, falling back to DOM renderer:', e)
      }
    }

    // Keep Sixel images in xterm's buffer so they follow terminal scrollback,
    // reflow, and screen-clearing semantics rather than a separate overlay.
    const imageAddon = new ImageAddon({
      pixelLimit: 4_000_000,
      sixelSupport: true,
      sixelSizeLimit: 8_000_000,
      storageLimit: 24,
      showPlaceholder: true,
      iipSupport: false,
    })
    term.loadAddon(imageAddon)
    imageAddonRef.current = imageAddon

    // Conditionally load Unicode graphemes addon (experimental)
    if (graphemesPrefRef.current) {
      try {
        const graphemesAddon = new UnicodeGraphemesAddon()
        term.loadAddon(graphemesAddon)
        graphemesAddonRef.current = graphemesAddon
        graphemesLoadedRef.current = true
      } catch (e) {
        console.warn('Unicode graphemes addon failed to load:', e)
      }
    }

    // Conditionally create predictive echo overlay (experimental)
    if (predictiveEchoPrefRef.current) {
      try {
        predictiveEchoRef.current = new PredictiveEcho(term)
      } catch (e) {
        console.warn('Predictive echo failed to initialize:', e)
      }
    }

    const helperTextarea = container.querySelector('textarea.xterm-helper-textarea') as HTMLTextAreaElement | null

    // Request clipboard-write permission early so OSC 52 writes may work directly
    requestClipboardPermission()

    // Cmd/Ctrl+C: copy selection to clipboard if text is selected,
    // otherwise let it pass through as SIGINT.
    // Cmd/Ctrl+B: force-send Ctrl+B so browser shortcuts can't steal it.
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
          navigator.clipboard?.writeText(normalizeSelection(selection))
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
      // don't let xterm forward the button-2 mouse report to the terminal. Capture
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
      // With terminal mouse mode on, plain drag goes to the terminal; only a Shift+drag
      // leaves an xterm-owned selection here. No selection -> pass through.
      const sel = term.getSelection()
      if (sel) setSelectionMenu({ x: e.clientX, y: e.clientY, text: normalizeSelection(sel) })
    }

    container.addEventListener('mousedown', onMouseDown, true)
    container.addEventListener('keydown', onKeyDown, true)
    window.addEventListener('keydown', onWindowKeyDownCapture, true)
    helperTextarea?.addEventListener('paste', onPaste)

    // Suppress browser's native context menu so right-click passes through to the terminal
    container.addEventListener('contextmenu', onContextMenu)
    listenerCleanupRef.current = () => {
      container.removeEventListener('mousedown', onMouseDown, true)
      container.removeEventListener('keydown', onKeyDown, true)
      window.removeEventListener('keydown', onWindowKeyDownCapture, true)
      helperTextarea?.removeEventListener('paste', onPaste)
      container.removeEventListener('contextmenu', onContextMenu)
    }

    // Fit terminal to container — retry a few times to handle layout settling.
    // Uses the same scroll-preserving logic as the exported fit() callback
    // because font-load and layout-settle refits fire AFTER replay data has
    // populated the scrollback buffer, and a raw fitAddon.fit() would
    // displace the viewport.
    const doFit = () => {
      try { fitPreservingScroll(term, fitAddon, container) } catch {}
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
    let wsUrl: string
    if (backend === 'daemon') {
      wsUrl = `${protocol}//${window.location.host}/ws/daemon-session?name=${encodeURIComponent(sessionName)}&cols=${cols}&rows=${rows}`
    } else if (sessionName.startsWith('direct-pty:')) {
      wsUrl = `${protocol}//${window.location.host}/ws/direct-session?cols=${cols}&rows=${rows}`
    } else {
      const hostParam = hostId ? `&host=${encodeURIComponent(hostId)}` : ''
      wsUrl = `${protocol}//${window.location.host}/ws/session?name=${encodeURIComponent(sessionName)}&cols=${cols}&rows=${rows}${hostParam}`
    }
    const ws = new WebSocket(wsUrl)
    ws.binaryType = 'arraybuffer'
    wsRef.current = ws
    let sawFirstByte = false
    let msgCount = 0
    let totalBytes = 0
    let lastSummary = 0
    const tConnect = performance.now()

    // --- Phase 0 latency telemetry ---
    // inputToFrameMs: time from keypress to first inbound binary WebSocket
    // frame. This is a proxy for output round-trip, not proven per-key
    // authoritative echo. inputToWriteCompleteMs: time from keypress to the
    // xterm.write() callback, which indicates xterm completed its internal
    // write (not screen paint).
    const MAX_TELEMETRY_SAMPLES = 1000
    const TELEMETRY_INTERVAL_MS = 60000
    interface LatencySample {
      inputToFrameMs: number
      inputToWriteCompleteMs: number
    }
    const latencySamples: LatencySample[] = []
    let pendingInputTs: number | null = null
    let writePending = false
    let discardedInputs = 0

    function emitTelemetry(reason: string): void {
      if (activeConnId.current !== connId) return
      if (latencySamples.length === 0) return
      const sortedFrame = latencySamples.map(s => s.inputToFrameMs).sort((a, b) => a - b)
      const sortedWrite = latencySamples.map(s => s.inputToWriteCompleteMs).sort((a, b) => a - b)
      const p = (arr: number[], q: number) => arr[Math.floor(arr.length * q)] ?? 0
      console.debug('[termyard-telemetry]', reason, {
        samples: latencySamples.length,
        discarded: discardedInputs,
        inputToFrameMs: { p50: p(sortedFrame, 0.5), p90: p(sortedFrame, 0.9), p99: p(sortedFrame, 0.99) },
        inputToWriteCompleteMs: { p50: p(sortedWrite, 0.5), p90: p(sortedWrite, 0.9), p99: p(sortedWrite, 0.99) },
      })
    }

    telemetryIntervalRef.current = window.setInterval(() => emitTelemetry('periodic'), TELEMETRY_INTERVAL_MS)
    // --- end telemetry ---

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
        // Scroll guard: reliably keeps viewport at the bottom for 10s Uses `container`
        // (closure) not containerRef.current in case the ref is nulled
        // before the timer fires.
        if (!sawFirstByte) {
          sawFirstByte = true
          // Start a scroll guard that reliably keeps the viewport at
          // the bottom for 10 seconds after connect. Font-load and
          // layout reflows can reset scrollTop at unpredictable times.
          // The guard checks every 200ms and forces bottom. It stops
          // immediately if the user scrolls up (wheel/touch/keyboard).
          let userScrolled = false
          const onUserScroll = () => { userScrolled = true }
          container.addEventListener('wheel', onUserScroll, { passive: true })
          container.addEventListener('touchmove', onUserScroll, { passive: true })
          const guardInterval = window.setInterval(() => {
            if (userScrolled) {
              clearInterval(guardInterval)
              container.removeEventListener('wheel', onUserScroll)
              container.removeEventListener('touchmove', onUserScroll)
              return
            }
            term.scrollToBottom()
            const vp = container.querySelector('.xterm-viewport') as HTMLElement | null
            if (vp && vp.scrollTop + vp.clientHeight < vp.scrollHeight - 5) {
              vp.scrollTop = vp.scrollHeight
            }
          }, 200)
          // Auto-stop after 10s
          setTimeout(() => {
            clearInterval(guardInterval)
            container.removeEventListener('wheel', onUserScroll)
            container.removeEventListener('touchmove', onUserScroll)
          }, 10000)
        }
        const data = new Uint8Array(evt.data)
        const before = term.buffer.active.length

        // Clear prediction synchronously on any inbound authoritative
        // binary frame, before term.write.  An older async callback must
        // never erase a prediction created by a newer keystroke.
        predictiveEchoRef.current?.clear()

        // Phase 0 telemetry: measure input-to-output and input-to-paint
        if (pendingInputTs !== null) {
          const inputToFrameMs = performance.now() - pendingInputTs
          const capturedTs = pendingInputTs
          pendingInputTs = null
          writePending = true
          term.write(data, () => {
            const inputToWriteCompleteMs = performance.now() - capturedTs
            latencySamples.push({ inputToFrameMs, inputToWriteCompleteMs })
            if (latencySamples.length > MAX_TELEMETRY_SAMPLES) {
              latencySamples.shift()
            }
            writePending = false
          })
        } else {
          term.write(data)
        }
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
      if (telemetryIntervalRef.current) {
        clearInterval(telemetryIntervalRef.current)
        telemetryIntervalRef.current = undefined
      }
      emitTelemetry('disconnect')
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
        // Force bracket paste wrapping for multiline pastes when the
        // application hasn't enabled bracket paste mode (DECSET 2004).
        // Before v4 tmux handled this transparently; with direct PTY
        // sessions, apps like Pi that don't enable bracket paste mode
        // would see each pasted line as a separate Enter.
        if (
          data.length > 1 &&
          !data.startsWith('\x1b[200~') &&
          (data.includes('\r') || data.includes('\n'))
        ) {
          data = '\x1b[200~' + data + '\x1b[201~'
        }
        const encoder = new TextEncoder()
        // Phase 0 telemetry: arm latency measurement on eligible printable
        // ASCII input only when the WebSocket is open and no sample is pending.
        if (data.length === 1) {
          const code = data.charCodeAt(0)
          if (code >= 0x20 && code <= 0x7e) {
            if (pendingInputTs === null && !writePending) {
              pendingInputTs = performance.now()
            } else {
              discardedInputs++
            }
          }
        }
        // Predictive echo: render unconfirmed keystrokes before sending.
        // Lazily create the instance if the preference was toggled on after
        // the terminal was already connected (no reconnect needed).
        let pe = predictiveEchoRef.current
        if (!pe && predictiveEchoPrefRef.current && termRef.current) {
          try {
            pe = new PredictiveEcho(termRef.current)
            predictiveEchoRef.current = pe
          } catch (e) {
            console.warn('Predictive echo failed to initialize:', e)
          }
        }
        if (pe && predictiveEchoPrefRef.current) {
          if (pe.canPredict(data)) {
            pe.predict(data)
          } else {
            pe.clear()
          }
        }
        ws.send(encoder.encode(data))
      }
    })

    // Send resize events as JSON text messages
    term.onResize(({ cols, rows }) => {
      predictiveEchoRef.current?.clear()
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    })
  }, [sessionName, hostId, backend, cleanupWs, prefs.theme, prefs.terminal.font_size, prefs.terminal.font_family, prefs.terminal.scrollback, sendRawBytes])

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
    if (telemetryIntervalRef.current) {
      clearInterval(telemetryIntervalRef.current)
      telemetryIntervalRef.current = undefined
    }
    cleanupWs()
    if (webglAddonRef.current) {
      webglAddonRef.current.dispose()
      webglAddonRef.current = null
    }
    if (graphemesAddonRef.current) {
      graphemesAddonRef.current.dispose()
      graphemesAddonRef.current = null
      graphemesLoadedRef.current = false
    }
    if (predictiveEchoRef.current) {
      predictiveEchoRef.current.dispose()
      predictiveEchoRef.current = null
    }
    if (imageAddonRef.current) {
      imageAddonRef.current.dispose()
      imageAddonRef.current = null
    }
    if (termRef.current) {
      listenerCleanupRef.current?.()
      listenerCleanupRef.current = null
      termRef.current.dispose()
      termRef.current = null
    }
    containerRef.current = null
  }, [cleanupWs])

  const reconfigure = useCallback((renderer: string, graphemes: boolean, predictiveEcho: boolean) => {
    const term = termRef.current
    if (!term) return

    // Handle renderer change on the live terminal
    if (renderer === 'webgl' && !webglAddonRef.current) {
      try {
        const webglAddon = new WebglAddon()
        webglAddon.onContextLoss(() => {
          webglAddon.dispose()
          webglAddonRef.current = null
        })
        term.loadAddon(webglAddon)
        webglAddonRef.current = webglAddon
      } catch (e) {
        console.warn('WebGL addon failed to load, falling back to DOM renderer:', e)
      }
    } else if (renderer === 'dom' && webglAddonRef.current) {
      webglAddonRef.current.dispose()
      webglAddonRef.current = null
    }

    // Handle graphemes change — dispose on disable, load on enable
    if (graphemes && !graphemesLoadedRef.current) {
      try {
        const graphemesAddon = new UnicodeGraphemesAddon()
        term.loadAddon(graphemesAddon)
        graphemesAddonRef.current = graphemesAddon
        graphemesLoadedRef.current = true
      } catch (e) {
        console.warn('Unicode graphemes addon failed to load:', e)
      }
    } else if (!graphemes && graphemesAddonRef.current) {
      graphemesAddonRef.current.dispose()
      graphemesAddonRef.current = null
      graphemesLoadedRef.current = false
    }

    // Handle predictive echo change — dispose on disable, lazy-create on enable.
    if (predictiveEcho && !predictiveEchoRef.current) {
      try {
        predictiveEchoRef.current = new PredictiveEcho(term)
      } catch (e) {
        console.warn('Predictive echo failed to initialize:', e)
      }
    } else if (!predictiveEcho && predictiveEchoRef.current) {
      predictiveEchoRef.current.dispose()
      predictiveEchoRef.current = null
    }
  }, [])

  const fit = useCallback(() => {
    const term = termRef.current
    const container = containerRef.current
    if (!term || !fitAddonRef.current || !container) return
    fitPreservingScroll(term, fitAddonRef.current, container, { refreshAfter: true })
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
    reconfigure,
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
