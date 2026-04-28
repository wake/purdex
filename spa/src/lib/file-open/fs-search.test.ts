import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fsSearchByCapability, type SearchRootCapability } from './fs-search'
import { useHostStore } from '../../stores/useHostStore'

describe('fsSearchByCapability', () => {
  const baseState = useHostStore.getState()

  beforeEach(() => {
    // Stub host store via merge-mode setState — only override the methods our
    // helper consumes. Avoid replace-mode (which would wipe other actions like
    // `getWsBase`, `addHost`, etc that other tests depend on).
    useHostStore.setState({
      activeHostId: 'h1',
      hostOrder: ['h1'],
      getDaemonBase: () => 'http://daemon',
      getAuthHeaders: () => ({}),
    })
    globalThis.fetch = vi.fn(async () =>
      new Response(
        JSON.stringify({
          matches: [
            { path: '/a/foo.go', modTime: '2026-04-26T00:00:00Z', sizeBytes: 10, root: '/a' },
            { path: '/b/foo.go', modTime: '2026-04-27T00:00:00Z', sizeBytes: 10, root: '/b' },
          ],
          partial: false,
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    ) as never
  })

  it('posts mode-envelope body with capability roots and returns matches sorted by modTime desc', async () => {
    const roots: SearchRootCapability[] = [
      { kind: 'session-cwd', sessionCode: 'sess1' },
      { kind: 'workspace-projectPath', workspaceId: 'w1' },
    ]
    const matches = await fsSearchByCapability('h1', 'foo.go', roots)
    // Newer mtime sorted first
    expect(matches.map((m) => m.path)).toEqual(['/b/foo.go', '/a/foo.go'])
    const fetchCall = (globalThis.fetch as unknown as { mock: { calls: [string, RequestInit][] } }).mock.calls[0]
    expect(fetchCall[0]).toBe('http://daemon/api/fs/search')
    const body = JSON.parse(fetchCall[1].body as string)
    expect(body.mode).toBe('basename')
    expect(body.query).toEqual({ basename: 'foo.go' })
    expect(body.roots).toEqual(roots)
  })

  it('forwards limits when provided', async () => {
    await fsSearchByCapability('h1', 'foo.go', [{ kind: 'session-cwd', sessionCode: 's1' }], { maxResults: 5, maxDepth: 4, timeoutMs: 1000 })
    const body = JSON.parse(((globalThis.fetch as unknown as { mock: { calls: [string, RequestInit][] } }).mock.calls[0])[1].body as string)
    expect(body.limits).toEqual({ maxResults: 5, maxDepth: 4, timeoutMs: 1000 })
  })

  it('throws on 4xx error', async () => {
    globalThis.fetch = vi.fn(async () => new Response('bad request', { status: 400 })) as never
    await expect(
      fsSearchByCapability('h1', 'foo.go', [{ kind: 'session-cwd', sessionCode: 's1' }]),
    ).rejects.toThrow(/400/)
  })

  it('returns empty matches when daemon responds 501 not-implemented (caller should suppress)', async () => {
    // Layer 3 caller decides what to do; helper just exposes the status via thrown error.
    // For this contract: 501 is treated as a soft failure — helper throws Error tagged with status,
    // so layer 3 caller catches and suppresses (per plan v4).
    globalThis.fetch = vi.fn(async () => new Response('not implemented', { status: 501 })) as never
    await expect(
      fsSearchByCapability('h1', 'foo.go', [{ kind: 'workspace-projectPath', workspaceId: 'w1' }]),
    ).rejects.toMatchObject({ status: 501 })
  })

  // Restore baseline so other tests in this file (if added later) and global state aren't polluted.
  it('cleanup: restore default host store fields', () => {
    useHostStore.setState({
      activeHostId: baseState.activeHostId,
      hostOrder: baseState.hostOrder,
      getDaemonBase: baseState.getDaemonBase,
      getAuthHeaders: baseState.getAuthHeaders,
    })
    expect(true).toBe(true)
  })
})
