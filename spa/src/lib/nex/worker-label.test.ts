import { describe, it, expect } from 'vitest'
import { workerLabel } from './worker-label'

describe('workerLabel', () => {
  it('prefers session_title.text', () => {
    expect(workerLabel({ id: 'E1', brief: 'x', session_title: { text: '修 bug', source: 'ai' } })).toBe('修 bug')
  })
  it('falls back to the first non-empty line of the brief', () => {
    expect(workerLabel({ id: 'E1', brief: '\n  \nfirst\nsecond' })).toBe('first')
  })
  it('falls back to the id', () => {
    expect(workerLabel({ id: 'E1', brief: ' \n ' })).toBe('E1')
    expect(workerLabel({ id: 'E1', brief: '' })).toBe('E1')
  })
})
