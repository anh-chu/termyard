// Document Picture-in-Picture: pop a DOM node into a floating window.

// Why PiP may be unavailable. Returns null when usable, else a user-facing reason.
// Secure-context check comes first: documentPictureInPicture only exists in one,
// so the TLS/localhost hint is more actionable than a generic browser message.
export function pipUnavailableReason(): string | null {
  if (!window.isSecureContext)
    return 'Pop-out needs a secure connection. Open Termyard via localhost, or serve with TLS (--tls).'
  if (!('documentPictureInPicture' in window))
    return 'This browser does not support pop-out windows. Use Chrome/Edge or Firefox 151+.'
  return null
}
// Move `node` into `dest` via appendChild. Reports whether the owner
// document changed (cross-document move). Never clones; never mutates xterm.
export function transferNode(
  node: HTMLElement,
  dest: HTMLElement,
): { crossedDocument: boolean } {
  const crossedDocument = node.ownerDocument !== dest.ownerDocument
  dest.appendChild(node)
  return { crossedDocument }
}

// Move `node` into a PiP window. Returns restore metadata plus control.
// Caller keeps a placeholder in the page; restore moves `node` back to `home`.
export async function popOut(
  node: HTMLElement,
  home: HTMLElement,
  opts?: { width?: number; height?: number; onRestore?: () => void },
): Promise<{
  restore: () => void
  pipWindow: Window
  home: HTMLElement
}> {
  // @ts-expect-error: not in lib.dom yet
  const pip: Window = await window.documentPictureInPicture.requestWindow({
    width: opts?.width ?? 800,
    height: opts?.height ?? 600,
  })

  // Copy every stylesheet/inline style so Tailwind + xterm.css render.
  for (const sheet of Array.from(document.styleSheets)) {
    try {
      const css = Array.from(sheet.cssRules).map((r) => r.cssText).join('')
      const style = document.createElement('style')
      style.textContent = css
      pip.document.head.appendChild(style)
    } catch {
      // cross-origin sheet: re-link it
      const el = sheet.ownerNode as HTMLLinkElement | null
      if (el?.href) {
        const link = pip.document.createElement('link')
        link.rel = 'stylesheet'
        link.href = el.href
        pip.document.head.appendChild(link)
      }
    }
  }

  pip.document.body.style.margin = '0'
  transferNode(node, pip.document.body)

  let cleaned = false
  const restore = () => {
    if (cleaned) return
    cleaned = true
    transferNode(node, home)
    pip.close()
  }
  pip.addEventListener('pagehide', () => {
    restore()
    opts?.onRestore?.()
  }, { once: true })
  return { restore, pipWindow: pip, home }
}
