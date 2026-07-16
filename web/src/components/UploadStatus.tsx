import { cn } from '../lib/utils'
import type { UploadItem } from '../hooks/useFileUpload'

interface UploadStatusProps {
  uploads: UploadItem[]
  onCancel: (id: number) => void
  onDismiss: (id: number) => void
}

export function UploadStatus({ uploads, onCancel, onDismiss }: UploadStatusProps) {
  if (uploads.length === 0) return null

  return (
    <div className="absolute bottom-4 right-4 z-20 flex flex-col gap-2 max-w-sm w-72">
      {uploads.map((item) => (
        <div
          key={item.id}
          className="rounded-md border border-hairline bg-surface shadow-lg p-3 flex flex-col gap-1.5"
        >
          {/* Header row */}
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs font-medium text-ink truncate flex-1" title={item.name}>
              {item.name}
            </span>
            {item.status === 'uploading' && (
              <button
                type="button"
                onClick={() => onCancel(item.id)}
                className="flex-none text-mute hover:text-destructive transition-colors"
                title="Cancel upload"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                  <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
                </svg>
              </button>
            )}
            {item.status === 'done' && (
              <span className="flex-none text-[10px] font-bold uppercase tracking-widest text-primary">Uploaded</span>
            )}
          </div>

          {/* Progress bar (uploading) */}
          {item.status === 'uploading' && (
            <div className="flex flex-col gap-1">
              <div className="h-1.5 rounded-full bg-canvas overflow-hidden">
                <div
                  className={cn(
                    'h-full rounded-full bg-primary transition-[width] duration-150',
                    item.size === 0 && 'animate-pulse w-full'
                  )}
                  style={item.size > 0 ? { width: `${Math.round((item.sent / item.size) * 100)}%` } : undefined}
                />
              </div>
              <div className="text-[10px] text-mute/60 text-right">
                {item.size > 0
                  ? `${Math.round((item.sent / item.size) * 100)}%`
                  : '\u2026'}
              </div>
            </div>
          )}

          {/* Error */}
          {item.status === 'error' && (
            <div className="flex flex-col gap-1">
              <div className="text-xs text-destructive truncate" title={item.error}>
                {item.error}
              </div>
              <button
                type="button"
                onClick={() => onDismiss(item.id)}
                className="self-end text-[10px] font-bold uppercase tracking-widest text-mute hover:text-ink transition-colors"
              >
                Dismiss
              </button>
            </div>
          )}

          {/* Cancelled */}
          {item.status === 'cancelled' && (
            <div className="flex items-center justify-between">
              <span className="text-xs text-mute/60">Cancelled</span>
              <button
                type="button"
                onClick={() => onDismiss(item.id)}
                className="text-[10px] font-bold uppercase tracking-widest text-mute hover:text-ink transition-colors"
              >
                Dismiss
              </button>
            </div>
          )}

          {/* Copyable path (only when injection was skipped) */}
          {item.status === 'done' && item.injectionSkipped && item.quotedPath && (
            <div className="flex items-center gap-1.5">
              <code className="flex-1 text-[10px] font-mono text-mute/70 truncate" title={item.quotedPath}>
                {item.quotedPath}
              </code>
              <button
                type="button"
                onClick={() => {
                  navigator.clipboard?.writeText(item.quotedPath || '')
                }}
                className="flex-none text-[10px] font-bold uppercase tracking-widest text-mute hover:text-primary transition-colors"
                title="Copy path"
              >
                Copy
              </button>
            </div>
          )}
        </div>
      ))}
    </div>
  )
}
