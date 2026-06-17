import { useCallback, useEffect, useRef, useState } from 'react'

export interface UpdateStatus {
  current_version: string
  latest_version: string
  update_available: boolean
  pending_restart?: boolean
  channel: string
}

export function useSelfUpdate(wsStatus: UpdateStatus | null) {
  const [status, setStatus] = useState<UpdateStatus | null>(null)
  const [applying, setApplying] = useState(false)
  const [restartMode, setRestartMode] = useState<'auto' | 'manual' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const applyingRef = useRef(applying)
  const restartModeRef = useRef(restartMode)

  useEffect(() => {
    applyingRef.current = applying
  }, [applying])

  useEffect(() => {
    restartModeRef.current = restartMode
  }, [restartMode])

  useEffect(() => {
    let active = true
    fetch('/api/update')
      .then(async (res) => {
        if (!res.ok) return null
        return res.json() as Promise<UpdateStatus>
      })
      .then((data) => {
        if (!active || !data) return
        setStatus(data)
        if (data.pending_restart) {
          setRestartMode('manual')
        }
      })
      .catch(() => {})
    return () => { active = false }
  }, [])

  useEffect(() => {
    if (!wsStatus) return
    setStatus(wsStatus)
    setError(null)
    if (wsStatus.pending_restart) {
      setApplying(false)
      setRestartMode('manual')
      return
    }
    if (applyingRef.current || restartModeRef.current === 'auto' || restartModeRef.current === 'manual') {
      setApplying(false)
      setRestartMode(null)
    }
  }, [wsStatus])

  const apply = useCallback(async () => {
    if (applyingRef.current) return
    setApplying(true)
    setRestartMode(null)
    setError(null)

    try {
      const res = await fetch('/api/update/apply', { method: 'POST' })
      const text = await res.text()
      let data: { ok?: boolean; restarting?: boolean; error?: string; new_version?: string } = {}
      try {
        data = text ? JSON.parse(text) : {}
      } catch {
        data = {}
      }
      if (!res.ok) {
        throw new Error(data.error || text || `HTTP ${res.status}`)
      }
      setRestartMode(data.restarting ? 'auto' : 'manual')
      if (!data.restarting) {
        setApplying(false)
      }
    } catch (err) {
      setApplying(false)
      setRestartMode(null)
      setError(err instanceof Error ? err.message : 'update failed')
      throw err
    }
  }, [])

  return { status, applying, restartMode, error, apply }
}
