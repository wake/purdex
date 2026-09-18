// spa/src/lib/nex/session-slug.test.ts — the `{slug}` an execution's cwd
// maps to when the SPA names the tmux session a take-to-terminal creates
// (exec-to-terminal spec §4.2): the host project whose path is the cwd or its
// nearest ancestor wins; otherwise the cwd basename, cleaned.
import { describe, it, expect } from 'vitest'
import type { HostProject } from '../host-config-api'
import { fallbackSlugFor, slugForCwd } from './session-slug'

const project = (id: string, slug: string, path: string): HostProject => ({ id, name: id, slug, path })

describe('fallbackSlugFor', () => {
  it('is the cwd basename when it is already clean', () => {
    expect(fallbackSlugFor('/Users/w/Workspace/purdex')).toBe('purdex')
    expect(fallbackSlugFor('/srv/my_app-2')).toBe('my_app-2')
  })

  it('ignores a trailing slash', () => {
    expect(fallbackSlugFor('/Users/w/purdex/')).toBe('purdex')
  })

  it('replaces every character outside [a-zA-Z0-9_-] with "-", collapses runs and trims the ends', () => {
    expect(fallbackSlugFor('/tmp/my project (v2).x')).toBe('my-project-v2-x')
    expect(fallbackSlugFor('/tmp/--weird..name--')).toBe('weird-name')
    expect(fallbackSlugFor('/tmp/專案 目錄')).toBe('nex')
  })

  it('falls back to "nex" when nothing is left', () => {
    expect(fallbackSlugFor('/')).toBe('nex')
    expect(fallbackSlugFor('')).toBe('nex')
    expect(fallbackSlugFor('/tmp/...')).toBe('nex')
  })
})

describe('slugForCwd', () => {
  const projects = [
    project('a', 'purdex', '/Users/w/Workspace/wake/purdex'),
    project('b', 'wake-ws', '/Users/w/Workspace/wake'),
    project('c', 'ploom', '/Users/w/Workspace/wake/ploom/'),
  ]

  it('uses the slug of the project whose path equals the cwd', () => {
    expect(slugForCwd('/Users/w/Workspace/wake/purdex', projects)).toBe('purdex')
  })

  it('uses the nearest ancestor project when the cwd is inside one (worktree under the repo)', () => {
    expect(slugForCwd('/Users/w/Workspace/wake/purdex/.claude/worktrees/x', projects)).toBe('purdex')
    expect(slugForCwd('/Users/w/Workspace/wake/other', projects)).toBe('wake-ws')
  })

  it('matches on path segments, not on string prefix', () => {
    expect(slugForCwd('/Users/w/Workspace/wake/purdex-fork', projects)).toBe('wake-ws')
  })

  it('tolerates trailing slashes on either side', () => {
    expect(slugForCwd('/Users/w/Workspace/wake/ploom', projects)).toBe('ploom')
    expect(slugForCwd('/Users/w/Workspace/wake/ploom/sub/', projects)).toBe('ploom')
  })

  it('falls back to the cleaned basename when no project matches or the list is empty', () => {
    expect(slugForCwd('/srv/elsewhere/my app', projects)).toBe('my-app')
    expect(slugForCwd('/Users/w/Workspace/wake/purdex', [])).toBe('purdex')
  })

  it('expands a ~/… project with the host home and matches segment-wise (codex attacker F5)', () => {
    const tilde = [project('t', 'plm', '~/Workspace/wake/ploom'), project('u', 'ws', '~/w')]
    const home = '/Users/x'
    expect(slugForCwd('/Users/x/Workspace/wake/ploom', tilde, home)).toBe('plm')
    expect(slugForCwd('/Users/x/Workspace/wake/ploom/sub', tilde, home)).toBe('plm')
    expect(slugForCwd('/Users/x/w/repo', tilde, home)).toBe('ws')
    // `~/w` is `/Users/x/w`, not any `/w` segment somewhere below home.
    expect(slugForCwd('/Users/x/other/w/repo', tilde, home)).toBe('repo')
    // Another user's home is not this home.
    expect(slugForCwd('/Users/y/w/repo', tilde, home)).toBe('repo')
  })

  it('ignores ~/… projects when the host home is unknown (never guesses)', () => {
    const tilde = [project('t', 'plm', '~/Workspace/wake/ploom')]
    expect(slugForCwd('/Users/x/Workspace/wake/ploom', tilde)).toBe('ploom')
    expect(slugForCwd('/Users/x/Workspace/wake/ploom', tilde, '')).toBe('ploom')
  })

  it('falls back when the matching project has an empty slug', () => {
    expect(slugForCwd('/x/y', [project('e', '', '/x')])).toBe('y')
  })
})
