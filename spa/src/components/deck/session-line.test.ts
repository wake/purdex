import { describe, it, expect } from 'vitest'
import { formatSessionLine, repoOfCwd } from './session-line'

const WT = '/Users/wake/Workspace/wake/purdex/.claude/worktrees/iface-u1-3'

describe('repoOfCwd', () => {
  it('worktree path → the dir above .claude and the worktree name', () => {
    expect(repoOfCwd(WT)).toEqual({ repo: 'purdex', worktree: 'iface-u1-3' })
    expect(repoOfCwd(`${WT}/spa/src`)).toEqual({ repo: 'purdex', worktree: 'iface-u1-3' })
  })
  it('any other path → basename, trailing slash ignored', () => {
    expect(repoOfCwd('/a/b/mlab/')).toEqual({ repo: 'mlab', worktree: null })
    expect(repoOfCwd('/a/b/.claude')).toEqual({ repo: '.claude', worktree: null })
  })
  it('nothing → nothing', () => {
    expect(repoOfCwd(undefined)).toEqual({ repo: null, worktree: null })
    expect(repoOfCwd('  ')).toEqual({ repo: null, worktree: null })
  })
})

describe('formatSessionLine', () => {
  it('worktree + model + effort', () => {
    expect(formatSessionLine({ cwd: WT, model: 'Opus 5.5', effort: 'xhigh' })).toBe('purdex [iface-u1-3] [Opus 5.5 · xhigh]')
  })
  it('no worktree, no effort', () => {
    expect(formatSessionLine({ cwd: '/a/mlab', model: 'Opus 5.5', effort: null })).toBe('mlab [Opus 5.5]')
  })
  it('branch only when given, after the worktree', () => {
    expect(formatSessionLine({ cwd: WT, branch: 'main', model: 'M', effort: null })).toBe('purdex [iface-u1-3] (main) [M]')
  })
  it('nothing known → empty string', () => {
    expect(formatSessionLine({ cwd: null, model: null, effort: 'high' })).toBe('')
  })
})
