import { useState, useEffect, useCallback } from 'react'

export interface CrashedSession {
  id: string
  state: 'crashed'
  shell: string
  cwd: string
  cols: number
  rows: number
  created_at: string
  updated_at: string
  daemon_pid: number
  generation: string
}

export function useCrashedSessions() {
  const [crashedSessions, setCrashedSessions] = useState<CrashedSession[]>([])
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    try {
      const res = await fetch('/api/crashed-sessions')
      if (res.ok) {
        const data = await res.json()
        setCrashedSessions(data || [])
      }
    } catch (err) {
      console.error('Failed to fetch crashed sessions:', err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 10_000)
    return () => clearInterval(interval)
  }, [refresh])

  const recover = useCallback(async (id: string, overrides?: { shell?: string; cwd?: string }) => {
    try {
      const res = await fetch(`/api/crashed-sessions/${encodeURIComponent(id)}/recover`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ shell: overrides?.shell, cwd: overrides?.cwd }),
      })
      if (res.ok) {
        await refresh()
        return true
      }
      return false
    } catch (err) {
      console.error('Failed to recover crashed session:', err)
      return false
    }
  }, [refresh])

  const dismiss = useCallback(async (id: string) => {
    try {
      const res = await fetch(`/api/crashed-sessions/${encodeURIComponent(id)}`, {
        method: 'DELETE',
      })
      if (res.ok) {
        // Optimistic removal
        setCrashedSessions(prev => prev.filter(s => s.id !== id))
      }
    } catch (err) {
      console.error('Failed to dismiss crashed session:', err)
    }
  }, [])

  const dismissAll = useCallback(async () => {
    try {
      const res = await fetch('/api/crashed-sessions', { method: 'DELETE' })
      if (res.ok) {
        setCrashedSessions([])
      }
    } catch (err) {
      console.error('Failed to dismiss all crashed sessions:', err)
    }
  }, [])

  return { crashedSessions, loading, refresh, recover, dismiss, dismissAll }
}
