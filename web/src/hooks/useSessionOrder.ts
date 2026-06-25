import { useCallback, useEffect, useState } from 'react'

export type SessionOrderRanks = Record<string, string>

function toRanks(body: unknown): SessionOrderRanks {
  return body && typeof body === 'object' ? (body as SessionOrderRanks) : {}
}

export function useSessionOrder(authenticated: boolean) {
  const [ranks, setRanks] = useState<SessionOrderRanks>({})
  const [loaded, setLoaded] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const res = await fetch('/api/session-order')
      if (!res.ok) return
      const body = await res.json()
      setRanks(toRanks(body))
      setLoaded(true)
    } catch {}
  }, [])

  useEffect(() => {
    if (!authenticated) return
    refresh()
  }, [authenticated, refresh])

  const setRank = useCallback(async (key: string, rank: string) => {
    setRanks(prev => ({ ...prev, [key]: rank }))
    try {
      const res = await fetch('/api/session-order', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key, rank }),
      })
      if (!res.ok) return
      const body = await res.json()
      setRanks(toRanks(body))
      setLoaded(true)
    } catch {
      refresh()
    }
  }, [refresh])

  return { ranks, loaded, refresh, setRank }
}
