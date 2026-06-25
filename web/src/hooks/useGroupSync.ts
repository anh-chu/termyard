import { useCallback, useEffect, useState } from 'react'
import type { PaneTree } from '../lib/paneTree'

export type GroupRecord = {
  tree: PaneTree
  name?: string
  rank?: string
  deleted_at?: string | null
}

export type GroupRecordMap = Record<string, GroupRecord>

function toGroups(body: unknown): GroupRecordMap {
  return body && typeof body === 'object' ? (body as GroupRecordMap) : {}
}

export function useGroupSync(authenticated: boolean) {
  const [groups, setGroups] = useState<GroupRecordMap>({})
  const [loaded, setLoaded] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const res = await fetch('/api/groups')
      if (!res.ok) return
      const body = await res.json()
      setGroups(toGroups(body))
      setLoaded(true)
    } catch {}
  }, [])

  useEffect(() => {
    if (!authenticated) return
    refresh()
  }, [authenticated, refresh])

  const mutate = useCallback(async (body: Record<string, unknown>) => {
    const id = String(body.id)
    const op = String(body.op)
    setGroups(prev => {
      if (op === 'delete') {
        const next = { ...prev }
        delete next[id]
        return next
      }
      const current = prev[id] ?? { tree: { type: 'leaf', sessionKey: '' } as PaneTree }
      return { ...prev, [id]: { ...current, ...(body.tree !== undefined ? { tree: body.tree as PaneTree } : {}), ...(body.name !== undefined ? { name: body.name as string } : {}), ...(body.rank !== undefined ? { rank: body.rank as string } : {}) } }
    })
    try {
      const res = await fetch('/api/groups', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!res.ok) return
      const next = await res.json()
      setGroups(toGroups(next))
      setLoaded(true)
    } catch {
      refresh()
    }
  }, [refresh])

  const setTree = useCallback((id: string, tree: PaneTree) => mutate({ id, op: 'tree', tree }), [mutate])
  const setName = useCallback((id: string, name: string) => mutate({ id, op: 'name', name }), [mutate])
  const setRank = useCallback((id: string, rank: string) => mutate({ id, op: 'rank', rank }), [mutate])
  const deleteGroup = useCallback((id: string) => mutate({ id, op: 'delete' }), [mutate])

  return { groups, loaded, refresh, setTree, setName, setRank, deleteGroup }
}
