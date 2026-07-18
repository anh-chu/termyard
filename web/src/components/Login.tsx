import { useState, type FormEvent } from 'react'

interface LoginProps {
  mode: 'setup' | 'login'
  error: string | null
  onSubmit: (password: string) => Promise<boolean>
}

export function Login({ mode, error, onSubmit }: LoginProps) {
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [localError, setLocalError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const isSetup = mode === 'setup'
  const displayError = localError || error

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!password || submitting) return
    setLocalError(null)

    if (isSetup) {
      if (password.length < 8) {
        setLocalError('Password must be at least 8 characters')
        return
      }
      if (password !== confirm) {
        setLocalError('Passwords do not match')
        return
      }
    }

    setSubmitting(true)
    await onSubmit(password)
    setSubmitting(false)
  }

  return (
    <div className="flex items-center justify-center h-full w-full bg-canvas font-sans text-[13px] font-medium">
      <div className="w-full max-w-sm p-10 bg-surface border border-hairline rounded-xl">
        <div className="text-center mb-10">
          <div className="flex justify-center mb-6">
            <img src="/favicon.svg" alt="termyard" width="48" height="48" className="rounded-lg border border-hairline" />
          </div>
          <h1 className="text-2xl font-bold text-ink tracking-tight uppercase tracking-[0.2em]">Termyard</h1>
          <div className="flex flex-col gap-1 mt-4">
            <p className="text-xs font-bold text-mute/60 uppercase tracking-widest leading-relaxed">
              All sessions
            </p>
            <p className="text-xs font-bold text-mute/60 uppercase tracking-widest leading-relaxed">
              All AI agents
            </p>
            <p className="text-xs font-bold text-mute/60 uppercase tracking-widest leading-relaxed text-primary">
              One Interface
            </p>
          </div>
          {isSetup && (
            <p className="text-xs font-bold text-success/80 mt-6 uppercase tracking-widest">Set a password to initialize</p>
          )}
        </div>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={isSetup ? 'Choose password' : 'Password'}
              autoFocus
              className="w-full px-4 py-2.5 bg-surface-elevated border border-hairline rounded-sm text-ink placeholder:text-mute/40 outline-none focus:border-primary/60 transition-colors font-sans"
            />
          </div>
          {isSetup && (
            <div>
              <input
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                placeholder="Confirm password"
                className="w-full px-4 py-2.5 bg-surface-elevated border border-hairline rounded-sm text-ink placeholder:text-mute/40 outline-none focus:border-primary/60 transition-colors font-sans"
              />
            </div>
          )}
          {displayError && (
            <p className="text-xs font-bold text-destructive uppercase tracking-wide text-center">{displayError}</p>
          )}
          <button
            type="submit"
            disabled={submitting || !password || (isSetup && !confirm)}
            className="w-full px-4 py-3 bg-primary text-primary-foreground rounded-full font-bold uppercase tracking-widest hover:bg-white/90 disabled:opacity-30 disabled:cursor-not-allowed transition-all mt-2"
          >
            {submitting
              ? (isSetup ? 'Initializing...' : 'Verifying...')
              : (isSetup ? 'Set Password' : 'Enter Workspace')
            }
          </button>
        </form>
      </div>
    </div>
  )
}
