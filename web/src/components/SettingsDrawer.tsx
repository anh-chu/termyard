import { useEffect, useState } from 'react'
import { Settings } from './Settings'
import type { UpdateStatus } from '../hooks/useSelfUpdate'

type Bucket = 'look' | 'yard' | 'alerts' | 'network'

interface SettingsDrawerProps {
  open: boolean
  onClose: () => void
  pushState: string
  onPushSubscribe: () => void
  onPushUnsubscribe: () => void
  onLogout?: () => void
  version?: string | null
  updateAvailable?: boolean
  binaryUpdate?: UpdateStatus | null
  onApplyUpdate?: () => Promise<void>
  updateApplying?: boolean
  updateRestartMode?: 'auto' | 'manual' | null
  updateError?: string | null
}

const buckets: { id: Bucket; label: string }[] = [
  { id: 'look', label: 'Look' },
  { id: 'yard', label: 'Yard' },
  { id: 'alerts', label: 'Alerts' },
  { id: 'network', label: 'System' },
]

export function SettingsDrawer({ open, onClose, pushState, onPushSubscribe, onPushUnsubscribe, onLogout, version, updateAvailable, binaryUpdate, onApplyUpdate, updateApplying, updateRestartMode, updateError }: SettingsDrawerProps) {
  const [bucket, setBucket] = useState<Bucket>('look')

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-40">
      <div className="absolute inset-0 bg-black/30" onClick={onClose} />
      <div className="absolute right-0 top-0 h-full w-full sm:w-[min(560px,90vw)] bg-surface border-l border-hairline shadow-xl flex flex-col sm:flex-row">
        <button
          type="button"
          onClick={onClose}
          aria-label="Close settings"
          title="Close"
          className="absolute right-3 top-3 z-20 p-1.5 rounded-md text-mute hover:text-ink hover:bg-surface-elevated transition-colors"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>

        <nav className="flex flex-row gap-1 overflow-x-auto border-b border-hairline bg-surface-elevated p-2 pr-10 sm:w-28 sm:flex-col sm:gap-2 sm:border-b-0 sm:border-r sm:p-3 sm:pr-3">
          {buckets.map(b => (
            <button
              key={b.id}
              onClick={() => setBucket(b.id)}
              className={`shrink-0 rounded-sm px-3 py-2 text-left text-xs font-bold ${bucket === b.id ? 'bg-primary text-primary-foreground' : 'text-mute hover:text-ink hover:bg-surface'}`}
            >
              {b.label}
            </button>
          ))}
        </nav>
        <div className="flex-1 overflow-y-auto">
          <Settings
            bucket={bucket}
            pushState={pushState}
            onPushSubscribe={onPushSubscribe}
            onPushUnsubscribe={onPushUnsubscribe}
            onLogout={onLogout}
            version={version}
            updateAvailable={updateAvailable}
            binaryUpdate={binaryUpdate}
            onApplyUpdate={onApplyUpdate}
            updateApplying={updateApplying}
            updateRestartMode={updateRestartMode}
            updateError={updateError}
          />
        </div>
      </div>
    </div>
  )
}
