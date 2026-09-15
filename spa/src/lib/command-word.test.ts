import { describe, it, expect } from 'vitest'
import { commandWordOf } from './command-word'

describe('commandWordOf', () => {
  it.each([
    ['cld-yolo --resume {id}', 'cld-yolo'],
    ['  claude -c', 'claude'],
    ['OPENCODE_YOLO=true opencode -s {id}', 'opencode'],
    ['A=1 B=x=y codex resume', 'codex'],
    ['--flag value', '--flag'],
    ['./bin/run', './bin/run'],
    ['A=1 B=2', ''],
    ['', ''],
  ])('%j → %j', (template, word) => {
    expect(commandWordOf(template)).toBe(word)
  })
})
