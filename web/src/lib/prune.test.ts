import { describe, it, expect } from 'vitest'
import { PaneTree, replaceLeaf, getLeaves, findLeaf } from './paneTree'
import { sessionSnapshot, snapshotStable, pruneGroupTree, evaluateMissing } from './prune'

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

describe('evaluateMissing (grace window)', () => {
  const live = (...keys: string[]) => new Set(keys)
  const GRACE = 45_000

  it('does not expire a key that just went missing', () => {
    const m = new Map<string, number>()
    const r = evaluateMissing(['a', 'b'], live('a'), m, 1_000, GRACE)
    expect(r.expired.size).toBe(0)
    expect(m.get('b')).toBe(1_000)
    expect(r.nextDelayMs).toBe(GRACE)
  })

  it('expires only after the grace window fully elapses', () => {
    const m = new Map<string, number>()
    evaluateMissing(['a', 'b'], live('a'), m, 0, GRACE)
    const mid = evaluateMissing(['a', 'b'], live('a'), m, GRACE - 1, GRACE)
    expect(mid.expired.has('b')).toBe(false)
    expect(mid.nextDelayMs).toBe(1)
    const done = evaluateMissing(['a', 'b'], live('a'), m, GRACE, GRACE)
    expect(done.expired.has('b')).toBe(true)
    expect(done.nextDelayMs).toBeNull()
  })

  it('forgets a key that reappears within the grace window (transient recovery)', () => {
    const m = new Map<string, number>()
    evaluateMissing(['a', 'b'], live('a'), m, 0, GRACE)
    expect(m.has('b')).toBe(true)
    const back = evaluateMissing(['a', 'b'], live('a', 'b'), m, 10_000, GRACE)
    expect(back.expired.size).toBe(0)
    expect(m.has('b')).toBe(false)
  })

  it('clears bookkeeping for keys no longer tracked', () => {
    const m = new Map<string, number>([['stale', 100]])
    evaluateMissing(['a'], live('a'), m, 5_000, GRACE)
    expect(m.has('stale')).toBe(false)
  })

  it('reports the soonest pending window across multiple missing keys', () => {
    const m = new Map<string, number>()
    evaluateMissing(['a', 'b'], live(), m, 0, GRACE)
    const r = evaluateMissing(['a', 'b'], live(), m, 10_000, GRACE)
    expect(r.expired.size).toBe(0)
    expect(r.nextDelayMs).toBe(GRACE - 10_000)
  })
})
