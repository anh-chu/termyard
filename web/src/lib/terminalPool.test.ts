//
// terminalPool.test.ts — State-machine tests for TerminalPool
//
// Uses injectable fake factories. Does NOT construct real WebGL/xterm.
//

/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { TerminalPool, keyFor, __injectTransferNode } from './terminalPool'
import type { PoolFactory, PoolIdentity, TerminalPrefs, CheckoutCallbacks, LeaseToken } from './terminalPool'

// ── Counters ──────────────────────────────────────────────────────────

let terminalCreateCount = 0
let terminalDisposeCount = 0
let addonCreateCount = 0
let addonDisposeCount = 0
let socketOpenCount = 0
let socketCloseCount = 0
let socketSendCount = 0
let transferCallCount = 0

function resetCounters() {
  terminalCreateCount = 0
  terminalDisposeCount = 0
  addonCreateCount = 0
  addonDisposeCount = 0
  socketOpenCount = 0
  socketCloseCount = 0
  socketSendCount = 0
  transferCallCount = 0
}

// ── Fake HTMLElement ──────────────────────────────────────────────────

interface FakeEl extends HTMLElement {
  _fire(type: string, ...args: any[]): void
  _children: HTMLElement[]
  _parent: HTMLElement | null
}

function fakeEl(tag = 'div'): FakeEl {
  const children: HTMLElement[] = []
  let parent: HTMLElement | null = null
  const listeners = new Map<string, Array<(...args: any[]) => void>>()
  const styleObj: Record<string, string> = {}

  const el = {
    tagName: tag.toUpperCase(),
    _children: children,
    _parent: parent,

    get ownerDocument() {
      return {
        body: {
          appendChild(child: HTMLElement) {
            (child as FakeEl)._parent = el as any
            return child
          },
        } as any,
        defaultView: null,
      } as unknown as Document
    },

    appendChild(child: HTMLElement) {
      if ((child as FakeEl)._parent) {
        (child as FakeEl)._parent?.removeChild(child)
      }
      children.push(child)
      ;(child as FakeEl)._parent = el
      return child
    },
    removeChild(child: HTMLElement) {
      const idx = children.indexOf(child)
      if (idx > -1) { children.splice(idx, 1); (child as FakeEl)._parent = null }
      return child
    },
    remove() {
      if (parent) { parent.removeChild(el); parent = null }
    },
    get parentElement() { return parent },
    get children() { return children as unknown as HTMLCollection },

    style: {
      setProperty(k: string, v: string) { styleObj[k] = v },
      getPropertyValue(k: string) { return styleObj[k] || '' },
      position: '', top: '', left: '', width: '', height: '',
      overflow: '', pointerEvents: '', visibility: '', opacity: '',
    } as unknown as CSSStyleDeclaration,

    clientWidth: 800,
    clientHeight: 600,
    scrollTop: 0,

    querySelector(_sel: string): HTMLElement | null { return null },
    getElementsByClassName(): HTMLCollectionOf<Element> { return { length: 0, item: () => null, namedItem: () => null } as any },
    querySelectorAll(): NodeListOf<Element> { return { length: 0, item: () => null, forEach: () => {} } as any },

    addEventListener(type: string, fn: any, _opts?: any) {
      if (!listeners.has(type)) listeners.set(type, [])
      listeners.get(type)!.push(fn)
    },
    removeEventListener(type: string, fn: any) {
      const arr = listeners.get(type)
      if (arr) { const idx = arr.indexOf(fn); if (idx > -1) arr.splice(idx, 1) }
    },
    dispatchEvent(event: any) {
      const arr = listeners.get(event.type as string)
      arr?.forEach(fn => fn(event))
      return true
    },
    _fire(type: string, ...args: any[]) {
      const arr = listeners.get(type)
      arr?.forEach(fn => fn(...args))
    },
  } as unknown as FakeEl

  return el
}

// ── Fake Terminal ─────────────────────────────────────────────────────

class FakeTerminal {
  options: any
  element: HTMLElement
  cols = 80
  rows = 24
  buffer = {
    active: {
      type: 'normal' as const,
      cursorX: 0, cursorY: 0,
      baseY: 100, viewportY: 95, length: 100,
      getLine: (_i: number) => null,
    },
  }
  private _dataListeners: Array<(data: string) => void> = []
  private _resizeListeners: Array<(info: { cols: number; rows: number }) => void> = []

  constructor(options: any) {
    terminalCreateCount++
    this.options = options
    this.element = fakeEl()
  }

  loadAddon(_addon: any) { addonCreateCount++ }
  open(_container: HTMLElement) {}
  dispose() { terminalDisposeCount++ }
  onData(fn: (data: string) => void) {
    this._dataListeners.push(fn)
    return { dispose: () => {
      const idx = this._dataListeners.indexOf(fn)
      if (idx > -1) this._dataListeners.splice(idx, 1)
    }}
  }
  onResize(fn: (info: { cols: number; rows: number }) => void) {
    this._resizeListeners.push(fn)
    return { dispose: () => {
      const idx = this._resizeListeners.indexOf(fn)
      if (idx > -1) this._resizeListeners.splice(idx, 1)
    }}
  }
  onSelectionChange(_fn: () => void) { return { dispose: () => {} } }
  attachCustomKeyEventHandler(_fn: (e: any) => boolean) {}
  getSelection() { return '' }
  clearSelection() {}
  scrollToBottom() {}
  scrollLines(_delta: number) {}
  focus() {}
  refresh(_start: number, _end: number) {}
  write(_data: any, _cb?: () => void) {}

  _fireData(data: string) { this._dataListeners.forEach(fn => fn(data)) }
  _fireResize(cols: number, rows: number) {
    this.cols = cols; this.rows = rows
    this._resizeListeners.forEach(fn => fn({ cols, rows }))
  }
}

// ── Fake Addons ──────────────────────────────────────────────────────

class FakeFitAddon { fit() {} constructor() { addonCreateCount++ } }
class FakeWebLinksAddon { constructor() { addonCreateCount++ } }
class FakeClipboardAddon { constructor() { addonCreateCount++ } }
class FakeWebglAddon {
  private _onContextLoss: (() => void) | null = null
  constructor() { addonCreateCount++ }
  onContextLoss(fn: () => void) { this._onContextLoss = fn }
  dispose() { addonDisposeCount++ }
  _fireContextLoss() { this._onContextLoss?.() }
}
class FakeImageAddon { constructor() { addonCreateCount++ } dispose() { addonDisposeCount++ } }
class FakeUnicodeGraphemesAddon { constructor() { addonCreateCount++ } dispose() { addonDisposeCount++ } }
class FakePredictiveEcho {
  constructor() { addonCreateCount++ }
  canPredict(_data: string) { return false }
  predict(_char: string) {}
  clear() {}
  dispose() { addonDisposeCount++ }
}

// ── Fake WebSocket ───────────────────────────────────────────────────

class FakeWebSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 3

  readyState = FakeWebSocket.CONNECTING
  binaryType = 'arraybuffer'
  url: string
  onopen: ((evt?: any) => void) | null = null
  onclose: ((evt?: any) => void) | null = null
  onerror: ((evt?: any) => void) | null = null
  onmessage: ((evt?: any) => void) | null = null

  constructor(url: string) {
    socketOpenCount++
    this.url = url
  }

  send(_data: any) { socketSendCount++ }
  close() {
    socketCloseCount++
    this.readyState = FakeWebSocket.CLOSED
    this.onclose?.({ code: 1000, reason: '', wasClean: true })
  }

  _open() {
    this.readyState = FakeWebSocket.OPEN
    this.onopen?.({})
  }
  _message(data: any) {
    this.onmessage?.({ data })
  }
}

// ── Transfer ─────────────────────────────────────────────────────────

function fakeTransferNode(node: HTMLElement, dest: HTMLElement): { crossedDocument: boolean } {
  transferCallCount++
  // In tests we cannot call real jsdom appendChild with fake elements.
  // Track the call for assertions; pool code only needs crossedDocument.
  return { crossedDocument: false }
}

// ── Factory ──────────────────────────────────────────────────────────

function createFakeFactory(): PoolFactory {
  return {
    createTerminal: (options) => new FakeTerminal(options) as unknown as any,
    createFitAddon: () => new FakeFitAddon() as unknown as any,
    createWebLinksAddon: () => new FakeWebLinksAddon() as unknown as any,
    createClipboardAddon: () => new FakeClipboardAddon() as unknown as any,
    createWebglAddon: () => new FakeWebglAddon() as unknown as any,
    createImageAddon: () => new FakeImageAddon() as unknown as any,
    createUnicodeGraphemesAddon: () => new FakeUnicodeGraphemesAddon() as unknown as any,
    createPredictiveEcho: () => new FakePredictiveEcho() as unknown as any,
    createWebSocket: (url) => new FakeWebSocket(url) as unknown as WebSocket,
  }
}

// ── Test helpers ──────────────────────────────────────────────────────

function defPrefs(overrides?: Partial<TerminalPrefs>): TerminalPrefs {
  return {
    theme: 'dark', fontFamily: 'Space Mono', fontSize: 13,
    scrollback: 50000, renderer: 'dom',
    unicodeGraphemes: false, predictiveEcho: false,
    ...overrides,
  }
}

function defId(name = 'test-session', host?: string, backend?: string): PoolIdentity {
  return { sessionName: name, hostId: host, backend }
}

function noopCbs(): CheckoutCallbacks {
  return {
    onConnectionChange: () => {},
    onCtrlModifierChange: () => {},
    onAltModifierChange: () => {},
    onSelectionMenu: () => {},
  }
}

// ── Suite ────────────────────────────────────────────────────────────

describe('TerminalPool', () => {
  let pool: TerminalPool

  beforeEach(() => {
    resetCounters()
    pool = new TerminalPool(createFakeFactory())
    __injectTransferNode(fakeTransferNode)
  })

  afterEach(() => {
    pool.reset()
  })

  // ── Key normalization ──────────────────────────────────────────────

  it('normalizes keys: local session', () => {
    expect(keyFor('mysession', undefined)).toBe('mysession')
    expect(keyFor('mysession', '')).toBe('mysession')
    expect(TerminalPool.keyFor('mysession')).toBe('mysession')
  })

  it('normalizes keys: remote host', () => {
    expect(keyFor('mysession', 'host123')).toBe('host123/mysession')
  })

  // ── Cold checkout ──────────────────────────────────────────────────

  it('first checkout cold-creates one entry', () => {
    const container = fakeEl()
    const lease = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    expect(lease.key).toBe('s1')
    expect(lease.generation).toBe(1)
    expect(pool.size).toBe(1)
    expect(terminalCreateCount).toBe(1)
    expect(addonCreateCount).toBeGreaterThanOrEqual(4)
    expect(socketOpenCount).toBe(1)
  })

  it('cold checkout loads WebGL when prefs specify webgl', () => {
    const container = fakeEl()
    pool.checkout(defId('s1'), defPrefs({ renderer: 'webgl' }), container, noopCbs())
    expect(terminalCreateCount).toBe(1)
    expect(addonCreateCount).toBeGreaterThanOrEqual(5)
  })

  // ── Checkin keeps resources alive ──────────────────────────────────

  it('checkin does not dispose terminal/addons/socket', () => {
    const container = fakeEl()
    const lease = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    const tdB = terminalDisposeCount, adB = addonDisposeCount, scB = socketCloseCount
    pool.checkin(lease)
    expect(terminalDisposeCount).toBe(tdB)
    expect(addonDisposeCount).toBe(adB)
    expect(socketCloseCount).toBe(scB)
    expect(pool.size).toBe(1)
  })

  // ── Warm checkout ──────────────────────────────────────────────────

  it('repeat checkout reuses terminal (no new creates)', () => {
    const container = fakeEl()
    const l1 = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    pool.checkin(l1)
    const tcB = terminalCreateCount, scB = socketOpenCount
    const l2 = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    expect(l2.key).toBe('s1')
    expect(l2.generation).toBe(3)
    expect(terminalCreateCount).toBe(tcB)
    expect(socketOpenCount).toBe(scB)
  })

  // ── Different host ─────────────────────────────────────────────────

  it('different host creates distinct entry', () => {
    const container = fakeEl()
    pool.checkout(defId('s1', 'hostA'), defPrefs(), container, noopCbs())
    pool.checkout(defId('s1', 'hostB'), defPrefs(), container, noopCbs())
    expect(pool.size).toBe(2)
    expect(terminalCreateCount).toBe(2)
  })

  // ── Backend mismatch ───────────────────────────────────────────────

  it('backend mismatch disposes old and recreates', () => {
    const container = fakeEl()
    const l = pool.checkout(defId('s1', undefined, 'daemon'), defPrefs(), container, noopCbs())
    pool.checkin(l)
    const tdB = terminalDisposeCount
    pool.checkout(defId('s1', undefined, 'tmux'), defPrefs(), container, noopCbs())
    expect(terminalDisposeCount).toBeGreaterThan(tdB)
    expect(terminalCreateCount).toBe(2)
  })

  // ── Exclusive lease ────────────────────────────────────────────────

  it('latest lease wins: stale checkin no-op', () => {
    const container = fakeEl()
    const l1 = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    const l2 = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    expect(l2.generation).toBeGreaterThan(l1.generation)
    pool.checkin(l1) // stale
    expect(pool.size).toBe(1)
    pool.checkin(l2) // current
    expect(pool.size).toBe(1)
  })

  it('stale send does nothing', () => {
    const container = fakeEl()
    const l1 = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    pool.checkout(defId('s1'), defPrefs(), container, noopCbs()) // l2, invalidates l1
    const scB = socketSendCount
    pool.sendText(l1, 'hello')
    expect(socketSendCount).toBe(scB)
  })

  // ── Input gating ───────────────────────────────────────────────────

  it('active sends pass', () => {
    const container = fakeEl()
    const l = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    const entry = pool._debug_entry(l.key)
    ;(entry?.ws as unknown as FakeWebSocket)?._open()
    const scB = socketSendCount
    pool.sendText(l, 'hello')
    expect(socketSendCount).toBeGreaterThan(scB)
  })

  it('checked-in sends rejected', () => {
    const container = fakeEl()
    const l = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    // Open socket so sends can go through while lease is active
    const entry = pool._debug_entry(l.key)
    ;(entry?.ws as unknown as FakeWebSocket)?._open()
    // Send while active should work
    const scBeforeCheckin = socketSendCount
    pool.sendText(l, 'hello')
    expect(socketSendCount).toBeGreaterThan(scBeforeCheckin)

    pool.checkin(l)
    // After checkin, send should be rejected
    const scAfterCheckin = socketSendCount
    pool.sendText(l, 'hello')
    expect(socketSendCount).toBe(scAfterCheckin)
  })

  // ── Modifiers ──────────────────────────────────────────────────────

  it('stale sendImage rejected after checkin', async () => {
    const container = fakeEl()
    const l = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    const entry = pool._debug_entry(l.key)
    const ws = entry?.ws as unknown as FakeWebSocket
    ws?._open()

    // Create a small fake file
    const file = new File(['fake'], 'test.png', { type: 'image/png' })

    // Now checkin to invalidate lease
    pool.checkin(l)

    const scB = socketSendCount
    await pool.sendImage(l, file, 'image/png')
    // Should have been rejected — generation no longer matches
    expect(socketSendCount).toBe(scB)
  })

  it('modifier toggle persists through checkin/checkout', () => {
    const container = fakeEl()
    let ctrl = false
    const cbs1: CheckoutCallbacks = { ...noopCbs(), onCtrlModifierChange: (a) => { ctrl = a } }
    const l1 = pool.checkout(defId('s1'), defPrefs(), container, cbs1)
    pool.toggleCtrlModifier(l1)
    expect(ctrl).toBe(true)
    pool.checkin(l1)

    let ctrl2 = false
    const cbs2: CheckoutCallbacks = { ...noopCbs(), onCtrlModifierChange: (a) => { ctrl2 = a } }
    const l2 = pool.checkout(defId('s1'), defPrefs(), container, cbs2)
    expect(ctrl2).toBe(true)
    pool.clearCtrlModifier(l2)
    expect(ctrl2).toBe(false)
  })

  // ── Snapshot ───────────────────────────────────────────────────────

  it('getSnapshot null for unknown key', () => {
    expect(pool.getSnapshot('unknown')).toBeNull()
  })

  it('getSnapshot returns connection state after socket open', () => {
    const container = fakeEl()
    const l = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    expect(pool.getSnapshot(l.key)!.connected).toBe(false)
    const entry = pool._debug_entry(l.key)
    ;(entry?.ws as unknown as FakeWebSocket)?._open()
    expect(pool.getSnapshot(l.key)!.connected).toBe(true)
  })

  // ── WebGL context loss ─────────────────────────────────────────────

  it('WebGL context loss disposes only WebGL, keeps terminal', () => {
    const container = fakeEl()
    const l = pool.checkout(defId('s1'), defPrefs({ renderer: 'webgl' }), container, noopCbs())
    const entry = pool._debug_entry(l.key)
    const wgl = entry?.webglAddon as unknown as FakeWebglAddon
    expect(wgl).toBeTruthy()
    const tdB = terminalDisposeCount, scB = socketCloseCount, adB = addonDisposeCount
    wgl?._fireContextLoss()
    expect(addonDisposeCount).toBeGreaterThan(adB)
    expect(terminalDisposeCount).toBe(tdB)
    expect(socketCloseCount).toBe(scB)
  })

  // ── Prefs: theme/font preserve terminal ────────────────────────────

  it('theme/font update does not recreate terminal', () => {
    const container = fakeEl()
    const l = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    const tcB = terminalCreateCount
    pool.applyGlobalPrefs(defPrefs({ theme: 'light', fontSize: 16 }))
    expect(terminalCreateCount).toBe(tcB)
  })

  // ── Scrollback recreates ───────────────────────────────────────────

  it('scrollback change recreates', () => {
    const container = fakeEl()
    const l = pool.checkout(defId('s1'), defPrefs({ scrollback: 1000 }), container, noopCbs())
    const tdB = terminalDisposeCount, tcB = terminalCreateCount
    pool.applyGlobalPrefs(defPrefs({ scrollback: 2000 }))
    expect(terminalDisposeCount).toBeGreaterThan(tdB)
    expect(terminalCreateCount).toBeGreaterThan(tcB)
  })

  // ── Rekey ──────────────────────────────────────────────────────────

  it('rekey preserves terminal, updates map identity', () => {
    const container = fakeEl()
    const l = pool.checkout(defId('old', 'hostA'), defPrefs(), container, noopCbs())
    const tcB = terminalCreateCount, tdB = terminalDisposeCount
    pool.rekey('hostA/old', 'hostA/new')
    expect(terminalCreateCount).toBe(tcB)
    expect(terminalDisposeCount).toBe(tdB)
    expect(pool._debug_hasEntry('hostA/old')).toBe(false)
    expect(pool._debug_hasEntry('hostA/new')).toBe(true)
  })

  it('rekey collision disposes dest', () => {
    const container = fakeEl()
    const l1 = pool.checkout(defId('a'), defPrefs(), container, noopCbs())
    pool.checkin(l1)
    const l2 = pool.checkout(defId('b'), defPrefs(), container, noopCbs())
    pool.checkin(l2)
    const tdB = terminalDisposeCount
    pool.rekey('a', 'b')
    expect(terminalDisposeCount).toBeGreaterThan(tdB)
    expect(pool.size).toBe(1)
    expect(pool._debug_hasEntry('b')).toBe(true)
  })

  // ── Dispose ────────────────────────────────────────────────────────

  it('dispose tears down entry once', () => {
    const container = fakeEl()
    const l = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    pool.checkin(l)
    const tdB = terminalDisposeCount
    pool.dispose('s1')
    expect(terminalDisposeCount).toBeGreaterThan(tdB)
    expect(socketCloseCount).toBeGreaterThan(0)
    expect(pool.size).toBe(0)
  })

  it('dispose absent key no-ops', () => {
    const tdB = terminalDisposeCount
    pool.dispose('nonexistent')
    expect(terminalDisposeCount).toBe(tdB)
  })

  // ── disposeAbsent ─────────────────────────────────────────────────

  it('disposeAbsent removes only missing keys', () => {
    const container = fakeEl()
    const l1 = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    pool.checkin(l1)
    const l2 = pool.checkout(defId('s2'), defPrefs(), container, noopCbs())
    pool.checkin(l2)
    expect(pool.size).toBe(2)
    pool.disposeAbsent(new Set(['s1']))
    expect(pool.size).toBe(1)
    expect(pool._debug_hasEntry('s1')).toBe(true)
    expect(pool._debug_hasEntry('s2')).toBe(false)
  })

  it('disposeAbsent with all valid does nothing', () => {
    const container = fakeEl()
    pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    pool.disposeAbsent(new Set(['s1', 'extra']))
    expect(pool.size).toBe(1)
  })

  // ── Reset ──────────────────────────────────────────────────────────

  it('reset cleans all entries', () => {
    pool.checkout(defId('s1'), defPrefs(), fakeEl(), noopCbs())
    pool.checkout(defId('s2'), defPrefs(), fakeEl(), noopCbs())
    expect(pool.size).toBe(2)
    pool.reset()
    expect(pool.size).toBe(0)
    expect(terminalDisposeCount).toBe(2)
  })

  // ── Concurrent checkout no second terminal ─────────────────────────

  it('same-key concurrent checkout no second terminal', () => {
    const container = fakeEl()
    const l1 = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    const tcAfterFirst = terminalCreateCount
    const l2 = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    expect(terminalCreateCount).toBe(tcAfterFirst)  // no new terminal
    expect(l2.generation).toBeGreaterThan(l1.generation)
    expect(pool.size).toBe(1)
  })

  // ── No eviction ────────────────────────────────────────────────────

  it('no eviction regardless of entry count', () => {
    const container = fakeEl()
    for (let i = 0; i < 30; i++) {
      pool.checkout(defId(`s${i}`), defPrefs(), container, noopCbs())
    }
    expect(pool.size).toBe(30)
    for (let i = 0; i < 30; i++) {
      expect(pool._debug_hasEntry(`s${i}`)).toBe(true)
    }
  })

  // ── Pane close (checkin) retains entry ─────────────────────────────

  it('pane close equivalent (checkin) retains warm entry', () => {
    const container = fakeEl()
    const l = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    pool.checkin(l)
    expect(pool.size).toBe(1)
    expect(pool._debug_hasEntry('s1')).toBe(true)
    expect(terminalDisposeCount).toBe(0)
  })

  // ── Reconnect resize ───────────────────────────────────────────────

  it('checkout sends resize on open WS, checkin sends none, warm checkout sends resize', () => {
    const container = fakeEl()
    // First checkout creates entry with CONNECTING WS
    const l1 = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    const entry = pool._debug_entry(l1.key)
    const ws = entry?.ws as unknown as FakeWebSocket
    // Open the socket, which should trigger pending resize from onopen
    ws?._open()
    expect(socketSendCount).toBeGreaterThan(0)

    // Checkin — no resize sent
    pool.checkin(l1)
    const scAfterCheckin = socketSendCount

    // Second checkout (warm) — WS is already OPEN, checkout sends resize
    const l2 = pool.checkout(defId('s1'), defPrefs(), container, noopCbs())
    expect(socketSendCount).toBeGreaterThan(scAfterCheckin)
  })
})
