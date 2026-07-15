import type { Terminal, IMarker, IDecoration } from '@xterm/xterm'

/**
 * PredictiveEcho renders a disposable visual overlay of unconfirmed keystrokes
 * so typing feels immediate under high network latency.
 *
 * Constraints (by design):
 * - Only printable ASCII (0x20–0x7e) in the normal buffer before the last
 *   column.  Anything else clears the prediction immediately.
 * - Never calls term.write, never alters outbound bytes, and never reads
 *   terminal content. The PTY output is the sole authority.
 * - The overlay is a thin xterm decoration HTMLElement with pointer-events
 *   disabled and a dimmed/italic treatment; it is never mistaken for
 *   confirmed output.
 * - The prediction is cleared on the first authoritative binary output
 *   (term.write callback), resize, explicit dispose, or after a 500 ms
 *   timeout without any output.
 */
export class PredictiveEcho {
  private term: Terminal
  private pendingChars = ''
  private marker: IMarker | null = null
  private decoration: IDecoration | undefined
  private decorationEl: HTMLElement | null = null
  private timeoutId: ReturnType<typeof setTimeout> | undefined
  private disposed = false

  /** Maximum lifetime of a prediction without authoritative output. */
  private static readonly MAX_PENDING_MS = 500

  constructor(term: Terminal) {
    this.term = term
  }

  // ── public API ────────────────────────────────────────────────────────

  /** Returns true when `data` is a single printable ASCII character that
   *  can be safely predicted in the current terminal state. */
  canPredict(data: string): boolean {
    if (this.disposed) return false
    if (data.length !== 1) return false
    const code = data.charCodeAt(0)
    if (code < 0x20 || code > 0x7e) return false
    // Never predict inside alternate screen (vim, tmux control mode, fzf, …)
    if (this.term.buffer.active.type !== 'normal') return false
    // Cursor at last column would wrap the echo — don't predict.
    // Also reject when accumulated pending chars have already consumed
    // the second-to-last column (the next prediction would overflow).
    const effectiveX = this.term.buffer.active.cursorX + this.pendingChars.length
    if (effectiveX >= this.term.cols - 1) return false
    return true
  }

  /** Render (or append to) the predicted character visually.
   *  Safely rejects input that is no longer eligible (caller may have
   *  raced with terminal state changes). */
  predict(char: string): void {
    if (this.disposed) return
    if (!this.canPredict(char)) return
    this.pendingChars += char
    this.render()
    this.armTimeout()
  }

  /** Clear the prediction and dispose markers/decorations.
   *  Safe to call at any time; idempotent. */
  clear(): void {
    this.clearTimeout()
    this.pendingChars = ''
    this.disposeOverlay()
  }

  /** Full cleanup.  After this the instance must not be reused. */
  dispose(): void {
    this.disposed = true
    this.clear()
  }

  // ── internals ─────────────────────────────────────────────────────────

  private render(): void {
    // Tear down the previous overlay atomically before creating the new one.
    this.disposeOverlay()

    if (this.pendingChars.length === 0) return

    const buffer = this.term.buffer.active
    this.marker = this.term.registerMarker(0)
    if (!this.marker) return // marker registration can fail in edge cases

    this.decoration = this.term.registerDecoration({
      marker: this.marker,
      x: buffer.cursorX,
      width: this.pendingChars.length,
    })

    if (!this.decoration) {
      this.marker.dispose()
      this.marker = null
      return
    }

    const pending = this.pendingChars

    this.decoration.onRender((element) => {
      this.decorationEl = element
      this.applyStyle(element)
      element.textContent = pending
    })

    // If the element is already available synchronously, seed it right away
    // so we never show a decoration with empty content.
    if (this.decoration.element) {
      this.decorationEl = this.decoration.element
      this.applyStyle(this.decoration.element)
      this.decoration.element.textContent = pending
    }
  }

  private disposeOverlay(): void {
    if (this.decoration) {
      this.decoration.dispose()
      this.decoration = undefined
    }
    this.decorationEl = null

    if (this.marker) {
      this.marker.dispose()
      this.marker = null
    }
  }

  private applyStyle(el: HTMLElement): void {
    const opts = this.term.options
    el.style.fontFamily = opts.fontFamily ?? 'monospace'
    el.style.fontSize = `${opts.fontSize ?? 13}px`
    el.style.fontWeight = String(opts.fontWeight ?? 'normal')
    el.style.color = opts.theme?.foreground ?? 'inherit'
    el.style.opacity = '0.4'
    el.style.fontStyle = 'italic'
    el.style.pointerEvents = 'none'
    el.style.userSelect = 'none'
    el.style.whiteSpace = 'pre'
    el.style.overflow = 'hidden'
    el.style.display = 'flex'
    el.style.alignItems = 'center'
    el.style.height = '100%'
  }

  private armTimeout(): void {
    this.clearTimeout()
    this.timeoutId = setTimeout(() => this.clear(), PredictiveEcho.MAX_PENDING_MS)
  }

  private clearTimeout(): void {
    if (this.timeoutId !== undefined) {
      clearTimeout(this.timeoutId)
      this.timeoutId = undefined
    }
  }
}
