import { describe, it, expect } from 'vitest'
import { workerTabTitle } from './worker-tab-title'

describe('workerTabTitle (spec §8.4 / N1)', () => {
  it('session_title wins over the pre-handoff title and the brief', () => {
    expect(workerTabTitle({ sessionTitle: 'Named', fromTitle: 'From', brief: 'Brief', cwd: '/w/repo' })).toBe('Named - repo')
  })

  it('pre-handoff title wins over the brief when there is no session title', () => {
    expect(workerTabTitle({ sessionTitle: undefined, fromTitle: 'From', brief: 'Brief', cwd: '/w/repo' })).toBe('From - repo')
    expect(workerTabTitle({ sessionTitle: null, fromTitle: 'From', brief: 'Brief', cwd: '/w/repo' })).toBe('From - repo')
  })

  it('falls back to the first line of the brief', () => {
    expect(workerTabTitle({ brief: 'Fix the bug\nwith details', cwd: '/w/repo' })).toBe('Fix the bug - repo')
  })

  it('no cwd → just the primary', () => {
    expect(workerTabTitle({ brief: 'Fix the bug' })).toBe('Fix the bug')
    expect(workerTabTitle({ brief: 'Fix the bug', cwd: '' })).toBe('Fix the bug')
    expect(workerTabTitle({ fromTitle: 'From', cwd: '/' })).toBe('From')
  })

  it('a trailing slash on cwd still yields its basename', () => {
    expect(workerTabTitle({ brief: 'x', cwd: '/w/repo/' })).toBe('x - repo')
  })

  it('whitespace-only or empty sources are skipped', () => {
    expect(workerTabTitle({ sessionTitle: '  ', fromTitle: '\t', brief: 'Brief', cwd: '/w/repo' })).toBe('Brief - repo')
    expect(workerTabTitle({ sessionTitle: '', fromTitle: '', brief: 'Brief' })).toBe('Brief')
  })

  it('whitespace-only brief with nothing else → null', () => {
    expect(workerTabTitle({ brief: '   ', cwd: '/w/repo' })).toBeNull()
    expect(workerTabTitle({ brief: '\n\nsecond line', cwd: '/w/repo' })).toBeNull()
    expect(workerTabTitle({ cwd: '/w/repo' })).toBeNull()
    expect(workerTabTitle({})).toBeNull()
  })

  it('titles are sanitised to a single trimmed line', () => {
    expect(workerTabTitle({ fromTitle: '  Two\nlines  ', cwd: '/w/repo' })).toBe('Two - repo')
    expect(workerTabTitle({ sessionTitle: ' s\r\nx', brief: 'b' })).toBe('s')
  })
})
