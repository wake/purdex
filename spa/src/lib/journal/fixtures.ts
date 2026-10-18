// spa/src/lib/journal/fixtures.ts — wire-shaped fixtures written from the J-1e contract (daemon plan §8). Tests only.
const T0 = Date.UTC(2026, 9, 14, 6, 0, 0)

export const wireEntry = (id: string, over: Record<string, unknown> = {}): Record<string, unknown> => ({
  id, kind: 'exec', by: '', at: T0, actor: 'Adam', text: `did ${id}`, refs: [], pinned: false, ...over,
})

export const wirePage = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
  unit: { kind: 'team', id: 'tm1', label: 'Journal team' },
  project: { name: 'purdex', remote_url: 'https://github.com/wake/purdex' },
  person_name: 'Wake',
  entries: [wireEntry('wb:1:1', { at: T0 }), wireEntry('wb:2:1', { at: T0 + 1000 })],
  more: false,
  open_confirms: 0,
  ...over,
})

export const wireConfirm = (id: string, over: Record<string, unknown> = {}): Record<string, unknown> => wireEntry(id, {
  kind: 'confirm', actor: 'Kenji → Wake', text: 'questions', pinned: true,
  items: [{ text: 'git?', done: true }, { text: 'tasks?', done: false }], detail: 'the original question', ...over,
})

export const wireTasks = (): Record<string, unknown> => ({
  tasks: [
    { number: '7bcf08-12', title: 'Ship J-1', state: 'open', owner: 'Adam', refs: ['#2829'] },
    { number: 3, title: 'Old', state: 'done' },
    { number: '4', title: 'Cut', state: 'dropped', detail: 'no longer needed' },
  ],
})

export const wireRoadmap = (): Record<string, unknown> => ({
  features: [{ name: 'Journal', stages: [{ stage: 'J-1a', description: 'package', status: '完成' }, { stage: 'J-2a', description: 'data', status: '進行中' }] }],
})

export { T0 }
