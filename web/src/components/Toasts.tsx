import { useEffect } from 'react'

export interface Toast {
  id: number
  severity: 'error' | 'warn' | 'info'
  source: string
  message: string
  session?: string
}

interface ToastsProps {
  toasts: Toast[]
  onDismiss: (id: number) => void
}

const severityStyles: Record<Toast['severity'], string> = {
  error: 'border-destructive/50 bg-destructive/10 text-destructive',
  warn: 'border-amber-500/40 bg-amber-500/10 text-amber-500',
  info: 'border-border bg-card text-foreground',
}

function ToastItem({ toast, onDismiss }: { toast: Toast; onDismiss: (id: number) => void }) {
  useEffect(() => {
    const t = window.setTimeout(() => onDismiss(toast.id), toast.severity === 'error' ? 12000 : 8000)
    return () => window.clearTimeout(t)
  }, [toast.id, toast.severity, onDismiss])

  return (
    <div
      className={`pointer-events-auto w-80 rounded-md border px-3 py-2 shadow-lg backdrop-blur-sm ${severityStyles[toast.severity]}`}
      role="status"
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 text-xs font-medium uppercase tracking-wide opacity-80">
            <span>{toast.source}</span>
            {toast.session && <span className="truncate opacity-60">· {toast.session}</span>}
          </div>
          <div className="mt-0.5 break-words text-sm text-foreground/90">{toast.message}</div>
        </div>
        <button
          onClick={() => onDismiss(toast.id)}
          className="-mr-1 -mt-0.5 shrink-0 rounded px-1 text-foreground/50 hover:text-foreground"
          aria-label="Dismiss"
        >
          ×
        </button>
      </div>
    </div>
  )
}

export function Toasts({ toasts, onDismiss }: ToastsProps) {
  if (toasts.length === 0) return null
  return (
    <div className="pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2">
      {toasts.map(t => (
        <ToastItem key={t.id} toast={t} onDismiss={onDismiss} />
      ))}
    </div>
  )
}
