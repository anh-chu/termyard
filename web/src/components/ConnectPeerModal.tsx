import { useState, useRef, useEffect } from 'react'

interface Props {
  onClose: () => void
  onConnected: () => void
}

export function ConnectPeerModal({ onClose, onConnected }: Props) {
  const [address, setAddress] = useState('')
  const [password, setPassword] = useState('')
  const [autoReconnect, setAutoReconnect] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const addressRef = useRef<HTMLInputElement>(null)

  useEffect(() => { addressRef.current?.focus() }, [])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [onClose])

  const submit = async (e?: React.FormEvent) => {
    e?.preventDefault()
    setError(null)
    if (!address.trim()) { setError('Address is required'); return }
    if (!password) { setError('Password is required'); return }
    setSubmitting(true)
    try {
      const res = await fetch('/api/peers', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          address: address.trim(),
          password,
          auto_reconnect: autoReconnect,
        }),
      })
      if (!res.ok) {
        const body = await res.text().catch(() => '')
        let msg = body.trim() || `HTTP ${res.status}`
        if (res.status === 401) msg = 'Password rejected by remote machine'
        if (res.status === 503) msg = 'Remote machine has no password configured yet'
        setError(msg)
        setSubmitting(false)
        return
      }
      onConnected()
      onClose()
    } catch (err) {
      setError(`Could not reach ${address}. Check hostname and port.`)
      setSubmitting(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm"
      onClick={(e) => { if (e.target === e.currentTarget) onClose() }}
    >
      <form
        onSubmit={submit}
        className="w-full max-w-lg bg-canvas border border-hairline rounded-xl shadow-2xl overflow-hidden"
      >
        <div className="flex items-center justify-between px-5 py-4 border-b border-hairline">
          <div>
            <h2 className="text-sm font-bold text-ink tracking-tight">Connect to another machine</h2>
            <p className="text-xs text-mute mt-0.5">Establish a peer link using the other machine's dashboard password</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="w-7 h-7 flex items-center justify-center rounded-md text-mute hover:text-ink hover:bg-surface-elevated transition-colors text-lg leading-none"
          >
            ×
          </button>
        </div>

        <div className="p-5 flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-bold uppercase tracking-widest text-mute/70">Address</label>
            <input
              ref={addressRef}
              type="text"
              value={address}
              onChange={(e) => setAddress(e.target.value)}
              placeholder="devvm-b.local:7654"
              className="bg-surface-elevated border border-hairline rounded-sm px-3 py-2 text-[13px] font-medium text-ink outline-none focus:border-primary/60"
            />
            <p className="text-xs text-mute/60">Hostname or IP, port optional (default 7654).</p>
          </div>

          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-bold uppercase tracking-widest text-mute/70">Password</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="Dashboard password of the other machine"
              className="bg-surface-elevated border border-hairline rounded-sm px-3 py-2 text-[13px] font-medium text-ink outline-none focus:border-primary/60"
            />
            <p className="text-xs text-mute/60">Used once to establish trust, never sent again.</p>
          </div>

          <label className="flex items-center gap-3 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={autoReconnect}
              onChange={(e) => setAutoReconnect(e.target.checked)}
              className="w-4 h-4 accent-primary"
            />
            <span className="text-[13px] font-medium text-ink">Auto-reconnect if the connection drops</span>
          </label>

          {error && (
            <div className="text-xs font-medium text-destructive bg-destructive/10 border border-destructive/30 rounded-sm px-3 py-2">
              {error}
            </div>
          )}
        </div>

        <div className="flex justify-end gap-2 px-5 py-4 border-t border-hairline">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 rounded-md text-xs font-bold uppercase tracking-widest text-mute hover:text-ink hover:bg-surface-elevated transition-colors"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={submitting}
            className="px-5 py-2 rounded-md text-xs font-bold uppercase tracking-widest bg-primary text-primary-foreground hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {submitting ? 'Connecting…' : 'Connect'}
          </button>
        </div>
      </form>
    </div>
  )
}
