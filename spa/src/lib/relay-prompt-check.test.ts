import { describe, it, expect } from 'vitest'
import { checkRelayPromptBody, hasLoneSurrogate, relayPromptBytes } from './relay-prompt-check'

// The client mirror of internal/team/relay_prompts.go ValidateRelayPromptBody.
describe('checkRelayPromptBody', () => {
  it('16 384 UTF-8 bytes pass, 16 385 are too long (bytes, not characters)', () => {
    expect(checkRelayPromptBody('a'.repeat(16384))).toBeNull()
    expect(checkRelayPromptBody('a'.repeat(16385))).toBe('too_long')
    expect(relayPromptBytes('中')).toBe(3)
    expect(checkRelayPromptBody('中'.repeat(5462))).toBe('too_long') // 16 386 bytes, 5 462 characters
  })

  it.each([['\r'], ['\x00'], ['\x7f'], ['\u0085'], ['\x1b']])('control character %j is refused', (c) => {
    expect(checkRelayPromptBody(`a${c}b`)).toBe('control_chars')
  })

  it('newline and tab pass', () => {
    expect(checkRelayPromptBody('a\n\tb')).toBeNull()
  })

  it.each([['[pdx-relay'], ['x [pdx-relay:control] y'], ['line\n[pdx-relay seed op=1 n=2]']])('the tag %j is refused wherever it stands', (s) => {
    expect(checkRelayPromptBody(s)).toBe('has_tag')
  })

  // TextEncoder (and JSON into Go) turn an unpaired surrogate into U+FFFD: the daemon would store another text.
  it.each([
    ['a lone high surrogate', 'a\ud83db'],
    ['a lone high surrogate at the end', 'ab\ud83d'],
    ['a lone low surrogate', 'a\ude00b'],
    ['a low before its high', 'a\ude00\ud83db'],
  ])('%s is refused, before the bytes are counted', (_label, s) => {
    expect(hasLoneSurrogate(s)).toBe(true)
    expect(checkRelayPromptBody(s)).toBe('not_utf8')
    expect(checkRelayPromptBody(s + 'a'.repeat(16384))).toBe('not_utf8')
  })

  it('a surrogate pair (an emoji) is one character and passes', () => {
    expect(hasLoneSurrogate('ok 😀 😀')).toBe(false)
    expect(checkRelayPromptBody('ok 😀')).toBeNull()
    expect(relayPromptBytes('😀')).toBe(4)
  })
})
