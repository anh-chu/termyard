import { PaneTree, getLeaves, removeLeaf } from './paneTree'

// Stable, order-independent fingerprint of the live session key set. Used to
// detect when the session list has settled, so recovery transients (sessions
// briefly vanishing while tmux restarts) do not trigger a prune.
export function sessionSnapshot(keys: string[]): string {
  return [...keys].sort().join('\u0000')
}

// True once the same snapshot has been observed on consecutive updates and we
// are not mid-load. Pruning only acts on a settled list.
export function snapshotStable(prev: string, next: string, loading: boolean): boolean {
  return !loading && prev === next
}

export interface MissingEval {
  // Keys absent long enough to prune.
  expired: Set<string>
  // Soonest grace window still pending, in ms (null = nothing pending).
  nextDelayMs: number | null
}

// Track keys missing from the live session set. A tracked key must stay
// continuously absent for graceMs before it becomes eligible for pruning, so
// recovery transients (tmux restarts, peer-sync lag) never dissolve a group.
// `missingSince` is mutated in place: alive/untracked keys are cleared, newly
// missing keys get a first-seen timestamp.
export function evaluateMissing(
  trackedKeys: string[],
  liveKeys: Set<string>,
  missingSince: Map<string, number>,
  now: number,
  graceMs: number,
): MissingEval {
  const tracked = new Set(trackedKeys)
  for (const k of [...missingSince.keys()]) {
    if (!tracked.has(k) || liveKeys.has(k)) missingSince.delete(k)
  }
  const expired = new Set<string>()
  let nextDelayMs: number | null = null
  for (const k of tracked) {
    if (liveKeys.has(k)) continue
    let since = missingSince.get(k)
    if (since === undefined) { since = now; missingSince.set(k, now) }
    const elapsed = now - since
    if (elapsed >= graceMs) {
      expired.add(k)
    } else {
      const remaining = graceMs - elapsed
      nextDelayMs = nextDelayMs === null ? remaining : Math.min(nextDelayMs, remaining)
    }
  }
  return { expired, nextDelayMs }
}

// Prune leaves whose keys are gone from validKeys. Returns the updated group,
// or null when the group should dissolve (emptied, or down to a single leaf
// which reverts to a standalone session).
export function pruneGroupTree(
  tree: PaneTree,
  activeKey: string | null,
  validKeys: Set<string>,
): { tree: PaneTree; activeKey: string | null } | null {
  const toRemove = getLeaves(tree).filter(k => !validKeys.has(k))
  if (toRemove.length === 0) return { tree, activeKey }
  let next: PaneTree | null = tree
  for (const key of toRemove) {
    if (next) next = removeLeaf(next, key)
  }
  if (!next) return null
  if (getLeaves(next).length === 1) return null
  const newActiveKey = activeKey && validKeys.has(activeKey) ? activeKey : getLeaves(next)[0] ?? null
  return { tree: next, activeKey: newActiveKey }
}
