import { describe, it, expect } from 'vitest'
import { PaneTree, replaceLeaf, getLeaves, findLeaf } from './paneTree'
import { sessionSnapshot, snapshotStable, pruneGroupTree } from './prune'

const leaf = (k: string): PaneTree => ({ type: 'leaf', sessionKey: k })
const split = (a: PaneTree, b: PaneTree): PaneTree => ({ type: 'split', direction: 'h', ratio: 0.5, first: a, second: b })

describe('replaceLeaf (rename migration)', () => {
  it('rewrites the renamed key in place, leaving structure intact', () => {
    const tree = split(leaf('host/old'), leaf('host/b'))
    const next = replaceLeaf(tree, 'host/old', 'host/new')
    expect(getLeaves(next)).toEqual(['host/new', 'host/b'])
    expect(findLeaf(next, 'host/old')).toBe(false)
  })

  it('is a no-op when the old key is absent', () => {
    const tree = split(leaf('a'), leaf('b'))
    expect(replaceLeaf(tree, 'missing', 'x')).toEqual(tree)
  })
})

describe('snapshotStable (recovery transient gate)', () => {
  it('treats a transient drop as unstable, then stable once it settles', () => {
    const full = sessionSnapshot(['a', 'b'])
    const transient = sessionSnapshot(['a']) // b vanished mid-restart

    // full -> transient: list changed, do not prune
    expect(snapshotStable(full, transient, false)).toBe(false)
    // transient -> transient: settled, prune may proceed
    expect(snapshotStable(transient, transient, false)).toBe(true)
  })

  it('never reports stable while loading', () => {
    const s = sessionSnapshot(['a'])
    expect(snapshotStable(s, s, true)).toBe(false)
  })

  it('is order independent', () => {
    expect(sessionSnapshot(['b', 'a'])).toBe(sessionSnapshot(['a', 'b']))
  })
})

describe('pruneGroupTree', () => {
  const valid = (...keys: string[]) => new Set(keys)

  it('keeps a 3-leaf group when one of three goes away (still >1)', () => {
    const tree = split(leaf('a'), split(leaf('gone'), leaf('c')))
    const res = pruneGroupTree(tree, 'a', valid('a', 'c'))
    expect(res).not.toBeNull()
    expect(getLeaves(res!.tree).sort()).toEqual(['a', 'c'])
  })

  it('dissolves (null) when pruning leaves a single member', () => {
    const tree = split(leaf('a'), leaf('gone'))
    expect(pruneGroupTree(tree, 'a', valid('a'))).toBeNull()
  })

  it('returns the same group unchanged when all keys are valid', () => {
    const tree = split(leaf('a'), leaf('b'))
    const res = pruneGroupTree(tree, 'a', valid('a', 'b'))
    expect(res!.tree).toBe(tree)
    expect(res!.activeKey).toBe('a')
  })

  it('reassigns activeKey when the active leaf was pruned', () => {
    const tree = split(leaf('a'), split(leaf('gone'), leaf('c')))
    const res = pruneGroupTree(tree, 'gone', valid('a', 'c'))
    expect(res!.activeKey === 'a' || res!.activeKey === 'c').toBe(true)
  })
})
