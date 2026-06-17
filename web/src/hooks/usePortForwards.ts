import { useState, useEffect, useCallback } from 'react'

export type ForwardMode = 'proxy' | 'socat'

export interface PortForward {
  port: number
  label: string
  mode: ForwardMode
  external_port?: number
  base_url?: string
}

export function usePortForwards(hostId?: string) {
  const [forwards, setForwards] = useState<PortForward[]>([])
  const [loading, setLoading] = useState(true)
  const base = hostId ? `/api/hosts/${encodeURIComponent(hostId)}` : '/api'

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const res = await fetch(`${base}/portforwards`)
      if (res.ok) {
        const data = await res.json()
        setForwards(Array.isArray(data) ? data : [])
      }
    } catch {
      // silent
    } finally {
      setLoading(false)
    }
  }, [base])

  useEffect(() => {
    refresh()
  }, [refresh])

  const add = useCallback(async (port: number, label: string, mode: ForwardMode, externalPort?: number): Promise<string | null> => {
    try {
      const res = await fetch(`${base}/portforwards`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ port, label, mode, external_port: externalPort || 0 }),
      })
      if (res.ok) {
        const data = await res.json()
        setForwards(Array.isArray(data) ? data : [])
        return null
      }
      return await res.text()
    } catch (e) {
      return String(e)
    }
  }, [base])

  const remove = useCallback(async (port: number) => {
    const res = await fetch(`${base}/portforward/${port}`, { method: 'DELETE' })
    if (res.ok) {
      setForwards(prev => prev.filter(f => f.port !== port))
    }
  }, [base])

  return { forwards, loading, add, remove, refresh }
}
