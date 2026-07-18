import { useState } from 'react'
import { CrashedSession } from '../hooks/useCrashedSessions'
import { formatRelativeTime } from '../lib/time'

interface RecoveryPanelProps {
  crashedSessions: CrashedSession[]
  onRecover: (id: string, overrides?: { shell?: string; cwd?: string }) => Promise<boolean>
  onDismiss: (id: string) => Promise<void>
  onDismissAll: () => Promise<void>
  onClose?: () => void
}

interface SessionEdits {
  shell: string
  cwd: string
}

function RecoveryCard({
  session,
  onRecover,
  onDismiss,
}: {
  session: CrashedSession
  onRecover: (id: string, overrides?: { shell?: string; cwd?: string }) => Promise<boolean>
  onDismiss: (id: string) => Promise<void>
}) {
  const [edits, setEdits] = useState<SessionEdits>({ shell: session.shell, cwd: session.cwd })
  const [recovering, setRecovering] = useState(false)
  const [dismissing, setDismissing] = useState(false)
  const crashTime = formatRelativeTime(session.updated_at)

  const handleRecover = async () => {
    setRecovering(true)
    try {
      await onRecover(session.id, { shell: edits.shell || undefined, cwd: edits.cwd || undefined })
    } finally {
      setRecovering(false)
    }
  }

  const handleDismiss = async () => {
    setDismissing(true)
    try {
      await onDismiss(session.id)
    } finally {
      setDismissing(false)
    }
  }

  return (
    <div
      style={{
        background: 'var(--surface)',
        border: '1px solid var(--hairline)',
        borderRadius: '6px',
        padding: '14px',
        display: 'flex',
        flexDirection: 'column',
        gap: '10px',
      }}
    >
      {/* Header row: session name + crash time + dismiss */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', minWidth: 0 }}>
        <span style={{
          fontSize: '13px',
          fontWeight: 600,
          color: 'var(--ink)',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          flex: 1,
          minWidth: 0,
        }}>
          {session.id}
        </span>
        <span style={{
          fontSize: '11px',
          color: 'var(--mute)',
          whiteSpace: 'nowrap',
          flexShrink: 0,
        }}>
          crashed {crashTime}
        </span>
        <button
          type="button"
          disabled={dismissing}
          onClick={handleDismiss}
          style={{
            fontSize: '11px',
            fontWeight: 500,
            color: 'var(--mute)',
            background: 'transparent',
            border: '1px solid var(--hairline)',
            borderRadius: '4px',
            padding: '3px 8px',
            cursor: dismissing ? 'default' : 'pointer',
            opacity: dismissing ? 0.5 : 1,
            flexShrink: 0,
          }}
        >
          Dismiss
        </button>
      </div>

      {/* Editable fields */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
        <label style={{ display: 'flex', flexDirection: 'column', gap: '3px' }}>
          <span style={{ fontSize: '10px', fontWeight: 500, color: 'var(--mute)', textTransform: 'uppercase', letterSpacing: '0.5px' }}>
            Shell
          </span>
          <input
            type="text"
            value={edits.shell}
            onChange={(e) => setEdits(prev => ({ ...prev, shell: e.target.value }))}
            style={{
              fontSize: '12px',
              fontFamily: 'monospace',
              color: 'var(--ink)',
              background: 'var(--canvas)',
              border: '1px solid var(--hairline)',
              borderRadius: '4px',
              padding: '5px 8px',
              outline: 'none',
              width: '100%',
              boxSizing: 'border-box',
            }}
            spellCheck={false}
          />
        </label>
        <label style={{ display: 'flex', flexDirection: 'column', gap: '3px' }}>
          <span style={{ fontSize: '10px', fontWeight: 500, color: 'var(--mute)', textTransform: 'uppercase', letterSpacing: '0.5px' }}>
            Directory
          </span>
          <input
            type="text"
            value={edits.cwd}
            onChange={(e) => setEdits(prev => ({ ...prev, cwd: e.target.value }))}
            style={{
              fontSize: '12px',
              fontFamily: 'monospace',
              color: 'var(--ink)',
              background: 'var(--canvas)',
              border: '1px solid var(--hairline)',
              borderRadius: '4px',
              padding: '5px 8px',
              outline: 'none',
              width: '100%',
              boxSizing: 'border-box',
            }}
            spellCheck={false}
          />
        </label>
      </div>

      {/* Warning text */}
      <p style={{
        fontSize: '10px',
        color: 'var(--accent-yellow)',
        margin: 0,
        lineHeight: 1.4,
      }}>
        This starts a new shell; it cannot restore the previous process.
      </p>

      {/* Recover button */}
      <button
        type="button"
        disabled={recovering}
        onClick={handleRecover}
        style={{
          fontSize: '12px',
          fontWeight: 600,
          color: '#fff',
          background: 'var(--accent-green)',
          border: 'none',
          borderRadius: '4px',
          padding: '7px 14px',
          cursor: recovering ? 'default' : 'pointer',
          opacity: recovering ? 0.6 : 1,
          alignSelf: 'flex-start',
        }}
      >
        {recovering ? 'Recovering…' : 'Recover Session'}
      </button>
    </div>
  )
}

export function RecoveryPanel({ crashedSessions, onRecover, onDismiss, onDismissAll, onClose }: RecoveryPanelProps) {
  if (crashedSessions.length === 0) return null

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 100,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: 'rgba(0,0,0,0.6)',
        backdropFilter: 'blur(4px)',
        WebkitBackdropFilter: 'blur(4px)',
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget && onClose) onClose()
      }}
    >
      <div
        style={{
          background: 'var(--canvas)',
          border: '1px solid var(--hairline)',
          borderRadius: '10px',
          padding: '20px',
          maxWidth: '520px',
          width: 'calc(100vw - 40px)',
          maxHeight: '80vh',
          overflowY: 'auto',
          display: 'flex',
          flexDirection: 'column',
          gap: '14px',
        }}
      >
        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
            <h2 style={{
              fontSize: '15px',
              fontWeight: 700,
              color: 'var(--ink)',
              margin: 0,
            }}>
              Crashed Sessions
            </h2>
            <span style={{
              fontSize: '10px',
              fontWeight: 600,
              color: '#fff',
              background: 'var(--accent-red)',
              borderRadius: '10px',
              padding: '2px 8px',
            }}>
              {crashedSessions.length}
            </span>
          </div>
          <div style={{ display: 'flex', gap: '6px' }}>
            <button
              type="button"
              onClick={onDismissAll}
              style={{
                fontSize: '11px',
                fontWeight: 500,
                color: 'var(--accent-red)',
                background: 'transparent',
                border: '1px solid var(--accent-red)',
                borderRadius: '4px',
                padding: '4px 10px',
                cursor: 'pointer',
              }}
            >
              Clear All
            </button>
            {onClose && (
              <button
                type="button"
                onClick={onClose}
                style={{
                  fontSize: '11px',
                  fontWeight: 500,
                  color: 'var(--mute)',
                  background: 'transparent',
                  border: '1px solid var(--hairline)',
                  borderRadius: '4px',
                  padding: '4px 10px',
                  cursor: 'pointer',
                }}
              >
                ✕
              </button>
            )}
          </div>
        </div>

        {/* Session cards */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
          {crashedSessions.map(session => (
            <RecoveryCard
              key={session.id}
              session={session}
              onRecover={onRecover}
              onDismiss={onDismiss}
            />
          ))}
        </div>
      </div>
    </div>
  )
}
