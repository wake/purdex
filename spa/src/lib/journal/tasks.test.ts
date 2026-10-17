import { describe, it, expect } from 'vitest'
import { displayNumber, sortTasks, taskKeyOfEntry, taskKeyOfTask } from './tasks'
import type { JournalTask } from './types'

const task = (number: string, state: JournalTask['state'], over: Partial<JournalTask> = {}): JournalTask => ({ number, title: `t${number}`, state, owner: '', refs: [], detail: '', ...over })

describe('sortTasks', () => {
  it('puts open first, done next, dropped last, and keeps the file order inside a state', () => {
    const out = sortTasks([task('1', 'done'), task('2', 'dropped'), task('3', 'open'), task('4', 'done'), task('5', 'open')])
    expect(out.map((t) => t.number)).toEqual(['3', '5', '1', '4', '2'])
  })
  it('does not change the list it is given', () => {
    const list = [task('1', 'done'), task('2', 'open')]
    sortTasks(list)
    expect(list.map((t) => t.number)).toEqual(['1', '2'])
  })
})

describe('the tie between a task and its journal rows', () => {
  it('a team number, a solo number and a #-number all reduce to the seq', () => {
    expect(taskKeyOfTask({ number: '7bcf08-12' })).toBe('12')
    expect(taskKeyOfTask({ number: '12' })).toBe('12')
    expect(taskKeyOfTask({ number: '#12' })).toBe('12')
  })
  it('a task_new and a task_done entry carry the seq at the start of their text', () => {
    expect(taskKeyOfEntry({ kind: 'task_new', text: '#17 a task' })).toBe('17')
    expect(taskKeyOfEntry({ kind: 'task_done', text: '#17 a task' })).toBe('17')
    expect(taskKeyOfEntry({ kind: 'task_done', text: '#17' })).toBe('17')
  })
  it('any other kind, and a text that does not start with #seq, has no key', () => {
    expect(taskKeyOfEntry({ kind: 'exec', text: '#17 a task' })).toBeNull()
    expect(taskKeyOfEntry({ kind: 'comm', text: '#17 a task' })).toBeNull()
    expect(taskKeyOfEntry({ kind: 'task_new', text: 'fix #17' })).toBeNull()
    expect(taskKeyOfEntry({ kind: 'task_new', text: '#17abc' })).toBeNull()
  })
})

describe('displayNumber', () => {
  it('a solo task is #n, a team task is as the file has it', () => {
    expect(displayNumber({ number: '12' }, true)).toBe('#12')
    expect(displayNumber({ number: '#12' }, true)).toBe('#12')
    expect(displayNumber({ number: '7bcf08-12' }, false)).toBe('7bcf08-12')
  })
})
