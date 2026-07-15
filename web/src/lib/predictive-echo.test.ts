import { describe, it, expect, beforeEach, vi } from 'vitest'
import { PredictiveEcho } from './predictive-echo'
import type { Terminal, IMarker, IDecoration } from '@xterm/xterm'

/** Minimal fake HTMLElement-style object usable outside jsdom. */
function fakeEl(): HTMLElement {
  const style: Record<string, string> = {}
  const el = {
    style,
    textContent: '',
  } as unknown as HTMLElement
  return el
}

/** Create a minimal mock Terminal sufficient for PredictiveEcho. */
function mockTerm(overrides: {
  bufferType?: 'normal' | 'alternate'
  cursorX?: number
  cols?: number
} = {}): Terminal {
  const markerDisposed = { disposed: false }
  const decorationEl: HTMLElement = fakeEl()

  const marker: IMarker = {
    id: 1,
    line: 0,
    dispose: () => { markerDisposed.disposed = true },
    isDisposed: false,
    onDispose: (() => {}) as any,
  }

  const decoration: IDecoration = {
    marker,
    element: decorationEl,
    isDisposed: false,
    dispose: () => {},
    onDispose: (() => {}) as any,
    onRender: (() => {}) as any,
    options: {} as any,
  }

  return {
    buffer: {
      active: {
        type: overrides.bufferType ?? 'normal',
        cursorX: overrides.cursorX ?? 0,
        cursorY: 5,
        length: 0,
      },
    } as any,
    cols: overrides.cols ?? 80,
    rows: 24,
    options: {
      fontFamily: 'Test Mono',
      fontSize: 14,
      fontWeight: 'normal',
      theme: { foreground: '#cccccc' },
    },
    registerMarker: vi.fn().mockReturnValue(marker),
    registerDecoration: vi.fn().mockReturnValue(decoration),
  } as any
}

describe('PredictiveEcho', () => {
  let term: Terminal

  beforeEach(() => {
    term = mockTerm()
  })

  describe('canPredict', () => {
    it('accepts printable ASCII letters', () => {
      const pe = new PredictiveEcho(term)
      expect(pe.canPredict('a')).toBe(true)
      expect(pe.canPredict('Z')).toBe(true)
      expect(pe.canPredict('7')).toBe(true)
      expect(pe.canPredict(' ')).toBe(true) // space is 0x20
      expect(pe.canPredict('~')).toBe(true) // tilde is 0x7e
    })

    it('rejects non-printable control characters', () => {
      const pe = new PredictiveEcho(term)
      expect(pe.canPredict('\r')).toBe(false) // 0x0d
      expect(pe.canPredict('\n')).toBe(false)
      expect(pe.canPredict('\x7f')).toBe(false) // DEL
      expect(pe.canPredict('\x1b')).toBe(false) // ESC
      expect(pe.canPredict('\t')).toBe(false) // Tab
    })

    it('rejects multi-character strings (paste / IME)', () => {
      const pe = new PredictiveEcho(term)
      expect(pe.canPredict('hello')).toBe(false)
      expect(pe.canPredict('')).toBe(false)
    })

    it('rejects non-ASCII characters', () => {
      const pe = new PredictiveEcho(term)
      expect(pe.canPredict('é')).toBe(false)
      expect(pe.canPredict('漢')).toBe(false)
      expect(pe.canPredict('😀')).toBe(false)
    })

    it('rejects when in alternate screen buffer', () => {
      term = mockTerm({ bufferType: 'alternate' })
      const pe = new PredictiveEcho(term)
      expect(pe.canPredict('a')).toBe(false)
    })

    it('rejects at last column to avoid wrapping', () => {
      term = mockTerm({ cursorX: 79, cols: 80 })
      const pe = new PredictiveEcho(term)
      expect(pe.canPredict('a')).toBe(false)
    })

    it('accepts at second-to-last column', () => {
      term = mockTerm({ cursorX: 78, cols: 80 })
      const pe = new PredictiveEcho(term)
      expect(pe.canPredict('a')).toBe(true)
    })

    it('rejects after dispose', () => {
      const pe = new PredictiveEcho(term)
      pe.dispose()
      expect(pe.canPredict('a')).toBe(false)
    })

    it('rejects second char at second-to-last column (pending overflow)', () => {
      // cursorX 78 in an 80-col terminal: first char accepted,
      // then canPredict must reject the second because
      // cursorX + pendingChars.length + 1 >= cols.
      term = mockTerm({ cursorX: 78, cols: 80 })
      const pe = new PredictiveEcho(term)
      expect(pe.canPredict('a')).toBe(true)
      pe.predict('a') // pendingChars is now 1
      expect(pe.canPredict('b')).toBe(false)
    })
  })

  describe('predict / clear lifecycle', () => {
    it('clear() is idempotent', () => {
      const pe = new PredictiveEcho(term)
      pe.clear()
      pe.clear()
      // should not throw
    })

    it('dispose() prevents further prediction', () => {
      const pe = new PredictiveEcho(term)
      pe.dispose()
      expect(pe.canPredict('a')).toBe(false)
    })

    it('predict registers a marker and decoration', () => {
      const pe = new PredictiveEcho(term)
      pe.predict('x')
      expect(term.registerMarker).toHaveBeenCalled()
      expect(term.registerDecoration).toHaveBeenCalled()
    })

    it('clear disposes marker and decoration', () => {
      const pe = new PredictiveEcho(term)
      pe.predict('x')
      pe.clear()
      // After clear, a new predict creates fresh marker/decoration
      const callsBefore = (term.registerDecoration as any).mock.calls.length
      pe.predict('y')
      expect((term.registerDecoration as any).mock.calls.length).toBe(callsBefore + 1)
    })

    it('appends pending chars across multiple predict calls', () => {
      const pe = new PredictiveEcho(term)
      pe.predict('h')
      pe.predict('i')
      // The second predict should recreate the decoration with width 2
      const calls = (term.registerDecoration as any).mock.calls
      const lastCall = calls[calls.length - 1]
      expect(lastCall[0].width).toBe(2)
      expect(lastCall[0].x).toBe(0)
    })

    it('predict defensively rejects ineligible input', () => {
      term = mockTerm({ bufferType: 'alternate' })
      const pe = new PredictiveEcho(term)
      // ASCII, but alternate screen — predict must no-op
      const before = (term.registerDecoration as any).mock.calls.length
      pe.predict('a')
      expect((term.registerDecoration as any).mock.calls.length).toBe(before)
      // Also after dispose
      pe.dispose()
      pe.predict('a')
      expect((term.registerDecoration as any).mock.calls.length).toBe(before)
    })
  })

  describe('lifecycle: clear vs dispose contract', () => {
    it('clear() does NOT prevent future predictions (unlike dispose)', () => {
      // clear() resets pending state but leaves the instance reusable.
      // dispose() permanently disables the instance.
      const pe = new PredictiveEcho(term)
      pe.predict('a')
      pe.clear()
      // After clear, prediction is still allowed.
      expect(pe.canPredict('z')).toBe(true)
      pe.predict('z')
      // After dispose, prediction is permanently blocked.
      pe.dispose()
      expect(pe.canPredict('z')).toBe(false)
    })

    it('dispose then recreate: new instance works independently', () => {
      // Simulates the enable → disable → re-enable lifecycle.
      // Old instance is disposed; a brand-new instance must work.
      const pe1 = new PredictiveEcho(term)
      pe1.predict('x')
      pe1.dispose()

      const pe2 = new PredictiveEcho(term)
      expect(pe2.canPredict('y')).toBe(true)
      pe2.predict('y')
      const calls = (term.registerDecoration as any).mock.calls
      const lastCall = calls[calls.length - 1]
      expect(lastCall[0].width).toBe(1)
      expect(lastCall[0].x).toBe(0)

      // pe1 remains disposed
      expect(pe1.canPredict('z')).toBe(false)
    })

    it('dispose clears all overlays and pending state', () => {
      const pe = new PredictiveEcho(term)
      pe.predict('a')
      pe.predict('b')
      pe.dispose()
      // A new predict after dispose is a no-op (defensive).
      const before = (term.registerDecoration as any).mock.calls.length
      pe.predict('c')
      expect((term.registerDecoration as any).mock.calls.length).toBe(before)
    })
  })

  describe('decoration element styling', () => {
    it('applies dimmed italic style with pointer-events disabled', () => {
      const pe = new PredictiveEcho(term)
      pe.predict('?')
      // The mock returns a real HTMLElement on registerDecoration, so
      // decoration.element should have the styles applied immediately.
      const el = (term.registerDecoration as any).mock.results[0]?.value?.element
      expect(el).toBeDefined()
      expect(el.style.opacity).toBe('0.4')
      expect(el.style.fontStyle).toBe('italic')
      expect(el.style.pointerEvents).toBe('none')
      expect(el.style.userSelect).toBe('none')
      expect(el.textContent).toBe('?')
    })
  })
})
