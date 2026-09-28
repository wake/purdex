// spa/src/lib/nex/worker-upload.test.ts
import { describe, it, expect } from 'vitest'
import { canSend, composeWithAttachments, encodeImage, planAttachments, requestBytes, uploadErrorKey, type Chip } from './worker-upload'
import { selectImageAttachments } from '../../stores/useNexHostStore'
import { NexApiError } from './types'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'

const done = (key: string, path: string): Chip => ({ key, kind: 'path', name: path.split('/').pop()!, status: 'done', path })

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
    const chips: Chip[] = [done('1', '/w/a.txt'), { key: '2', kind: 'path', name: 'b', status: 'uploading' }, { key: '3', kind: 'path', name: 'c', status: 'failed', error: 'network' }]
    expect(composeWithAttachments('x', chips)).toBe('x\n\n[file: /w/a.txt]')
  })
})

describe('canSend', () => {
  it('is ok with no chips or only done chips', () => {
    expect(canSend([])).toEqual({ ok: true })
    expect(canSend([done('1', '/a')])).toEqual({ ok: true })
  })

  it('blocks while a chip is uploading', () => {
    expect(canSend([done('1', '/a'), { key: '2', kind: 'path', name: 'b', status: 'uploading' }])).toEqual({ ok: false, reason: 'uploading' })
  })

  it('blocks on a failed chip', () => {
    expect(canSend([{ key: '1', kind: 'path', name: 'a', status: 'failed', error: 'network' }])).toEqual({ ok: false, reason: 'failed' })
  })
})

describe('uploadErrorKey', () => {
  const codes = ['missing_file', 'invalid_execution_id', 'upload_dir_outside_cwd', 'execution_not_found',
    'execution_ended', 'cwd_unavailable', 'file_too_large', 'nex_unavailable', 'network',
    // Nexen send-with-attachments codes (contract §1.9) plus the request cap.
    'attachments_unsupported', 'too_many_attachments', 'invalid_attachment', 'attachment_type_unsupported',
    'attachment_too_large', 'attachments_too_large', 'request_too_large']

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

const MiB = 1024 * 1024
const caps = (over: Record<string, unknown> = {}) => ({
  media_types: ['image/png', 'image/jpeg', 'image/gif', 'image/webp'],
  max_bytes: 5 * MiB, max_count: 10, max_total_bytes: 20 * MiB, providers: ['claude'],
  fetch: { method: 'GET', path: '/api/nex/v1/executions/{id}/attachments/{sha256}' },
  maxRequestBytes: 32 * MiB, ...over,
})
const img = (key: string, size: number, type = 'image/png') => ({ key, size, type })

/** Standard base64 of `n` bytes, as the wire would carry it. */
const b64 = (n: number) => btoa(Array.from({ length: n }, (_, i) => String.fromCharCode(i % 251)).join(''))
const realBytes = (text: string, images: Array<{ size: number; type: string }>, lease = 'x'.repeat(64)) =>
  new TextEncoder().encode(JSON.stringify({
    lease_id: lease, text, attachments: images.map((i) => ({ type: 'image', media_type: i.type, data: b64(i.size) })),
  })).length

describe('requestBytes', () => {
  const texts = ['', 'hello', 'say "hi" \\ back\nand a\ttab', '看這張圖，引號「」與 emoji 🎉\n第二行', '\u0001 control']
  // Sizes cover size mod 3 = 0 / 1 / 2 (base64 padding).
  const sets = [[], [3], [4], [5], [1, 2, 3], [300, 301, 302]]
  for (const text of texts) {
    for (const sizes of sets) {
      it(`matches the serialized body for ${JSON.stringify(text)} with ${JSON.stringify(sizes)}`, () => {
        const images = sizes.map((size, i) => ({ size, type: i % 2 ? 'image/jpeg' : 'image/png' }))
        expect(requestBytes(text, images)).toBe(realBytes(text, images))
      })
    }
  }

  it('takes the lease id length into account', () => {
    const images = [{ size: 10, type: 'image/png' }]
    expect(requestBytes('x', images, 26)).toBe(realBytes('x', images, 'y'.repeat(26)))
  })
})

describe('planAttachments', () => {
  it('null caps (an older daemon) → every file by path (Review Focus 1)', () => {
    expect(planAttachments([img('a', 10), img('b', 20)], null, 'hi')).toEqual({ native: [], path: ['a', 'b'] })
  })

  it('a codex execution on a claude-only host: the selector yields null, so all go by path (Review Focus 2)', () => {
    const send = { delivery: [], max_text_bytes: 65536, max_request_bytes: 32 * MiB, attachments: { image: caps() } }
    const state = { byHost: { h: { phase: 'ready', capabilities: { send } } } } as never
    const files = [img('a', 10)]
    expect(planAttachments(files, selectImageAttachments('h', 'codex')(state), '')).toEqual({ native: [], path: ['a'] })
    expect(planAttachments(files, selectImageAttachments('h', 'claude')(state), '')).toEqual({ native: ['a'], path: [] })
  })

  it('six 4 MiB images: the first five go native (20 MiB total), the sixth by path (Review Focus 3)', () => {
    const files = ['1', '2', '3', '4', '5', '6'].map((k) => img(k, 4 * MiB))
    expect(planAttachments(files, caps(), 'look')).toEqual({ native: ['1', '2', '3', '4', '5'], path: ['6'] })
  })

  it('the total cap alone stops the sixth (a request budget with room to spare)', () => {
    const files = ['1', '2', '3', '4', '5', '6'].map((k) => img(k, 4 * MiB))
    expect(planAttachments(files, caps({ maxRequestBytes: 64 * MiB }), '')).toEqual({ native: ['1', '2', '3', '4', '5'], path: ['6'] })
    expect(planAttachments(files, caps({ maxRequestBytes: 64 * MiB, max_total_bytes: 24 * MiB }), '').native).toHaveLength(6)
  })

  it('a later smaller image still fits after a big one fell back', () => {
    const files = [img('a', 6 * MiB), img('b', 4 * MiB), img('c', 3 * MiB), img('d', 1 * MiB)]
    expect(planAttachments(files, caps(), '')).toEqual({ native: ['b', 'c', 'd'], path: ['a'] })
  })

  it('an unsupported type goes by path', () => {
    expect(planAttachments([img('a', 10, 'image/bmp'), img('b', 10)], caps(), '')).toEqual({ native: ['b'], path: ['a'] })
  })

  it('the per-image cap is inclusive', () => {
    expect(planAttachments([img('a', 5 * MiB), img('b', 5 * MiB + 1)], caps(), '')).toEqual({ native: ['a'], path: ['b'] })
  })

  it('max_count caps the native count', () => {
    const files = ['1', '2', '3'].map((k) => img(k, 10))
    expect(planAttachments(files, caps({ max_count: 2 }), '')).toEqual({ native: ['1', '2'], path: ['3'] })
  })

  it('the request-size cap: exactly at the limit fits, one byte under does not', () => {
    const files = [img('a', 1000)]
    const exact = requestBytes('hello', files)
    expect(planAttachments(files, caps({ maxRequestBytes: exact }), 'hello')).toEqual({ native: ['a'], path: [] })
    expect(planAttachments(files, caps({ maxRequestBytes: exact - 1 }), 'hello')).toEqual({ native: [], path: ['a'] })
  })

  it('the request-size cap counts the draft text', () => {
    const files = [img('a', 1000)]
    const limit = requestBytes('', files)
    expect(planAttachments(files, caps({ maxRequestBytes: limit }), '').native).toEqual(['a'])
    expect(planAttachments(files, caps({ maxRequestBytes: limit }), 'more text').native).toEqual([])
  })
})

describe('encodeImage', () => {
  it('returns standard padded base64 with no data: prefix, round-tripping the bytes', async () => {
    for (const n of [1, 2, 3, 4, 250, 1000]) {
      const bytes = new Uint8Array(n).map((_, i) => (i * 37 + 250) % 256)
      const out = await encodeImage(new File([bytes], 'p.png', { type: 'image/png' }))
      expect(out.startsWith('data:')).toBe(false)
      expect(out).toMatch(/^[A-Za-z0-9+/]*={0,2}$/)
      expect(out.length % 4).toBe(0)
      expect(Uint8Array.from(atob(out), (c) => c.charCodeAt(0))).toEqual(bytes)
    }
  })
})
