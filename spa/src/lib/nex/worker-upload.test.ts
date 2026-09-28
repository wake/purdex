// spa/src/lib/nex/worker-upload.test.ts
import { describe, it, expect } from 'vitest'
import { canSend, composeWithAttachments, uploadErrorKey, type Chip } from './worker-upload'
import { NexApiError } from './types'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'

const done = (key: string, path: string): Chip => ({ key, name: path.split('/').pop()!, status: 'done', path })

describe('composeWithAttachments', () => {
  it('returns the text unchanged with no chips', () => {
    expect(composeWithAttachments('hello', [])).toBe('hello')
  })

  it('appends one [file: path] line per done chip after a blank line', () => {
    expect(composeWithAttachments('look at these', [done('1', '/w/a.txt'), done('2', '/w/b c.png')]))
      .toBe('look at these\n\n[file: /w/a.txt]\n[file: /w/b c.png]')
  })

  it('returns only the file lines when there is no text', () => {
    expect(composeWithAttachments('', [done('1', '/w/a.txt')])).toBe('[file: /w/a.txt]')
  })

  it('skips chips that are not done', () => {
    const chips: Chip[] = [done('1', '/w/a.txt'), { key: '2', name: 'b', status: 'uploading' }, { key: '3', name: 'c', status: 'failed', error: 'network' }]
    expect(composeWithAttachments('x', chips)).toBe('x\n\n[file: /w/a.txt]')
  })
})

describe('canSend', () => {
  it('is ok with no chips or only done chips', () => {
    expect(canSend([])).toEqual({ ok: true })
    expect(canSend([done('1', '/a')])).toEqual({ ok: true })
  })

  it('blocks while a chip is uploading', () => {
    expect(canSend([done('1', '/a'), { key: '2', name: 'b', status: 'uploading' }])).toEqual({ ok: false, reason: 'uploading' })
  })

  it('blocks on a failed chip', () => {
    expect(canSend([{ key: '1', name: 'a', status: 'failed', error: 'network' }])).toEqual({ ok: false, reason: 'failed' })
  })
})

describe('uploadErrorKey', () => {
  const codes = ['missing_file', 'invalid_execution_id', 'upload_dir_outside_cwd', 'execution_not_found',
    'execution_ended', 'cwd_unavailable', 'file_too_large', 'nex_unavailable', 'network']

  it('maps each known daemon code to its own key, present in en and zh-TW', () => {
    for (const code of codes) {
      const key = uploadErrorKey(code)
      expect(key).toBe(`worker.upload.error.${code}`)
      expect((en as Record<string, string>)[key], key).toBeTruthy()
      expect((zhTW as Record<string, string>)[key], key).toBeTruthy()
    }
  })

  it('maps anything else to the generic key', () => {
    expect(uploadErrorKey('write_failed')).toBe('worker.upload.error.generic')
    expect(uploadErrorKey(undefined)).toBe('worker.upload.error.generic')
    expect((en as Record<string, string>)['worker.upload.error.generic']).toBeTruthy()
    expect((zhTW as Record<string, string>)['worker.upload.error.generic']).toBeTruthy()
  })

  it('reads the code off a NexApiError', () => {
    expect(uploadErrorKey(new NexApiError(413, 'file_too_large', 'x'))).toBe('worker.upload.error.file_too_large')
    expect(uploadErrorKey(new Error('boom'))).toBe('worker.upload.error.generic')
  })
})
