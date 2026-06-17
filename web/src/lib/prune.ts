import { PaneTree, getLeaves, removeLeaf } from './paneTree'

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
