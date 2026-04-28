import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { FileNotFoundPopup } from './FileNotFoundPopup'
import type { PopupSpec } from '../../../lib/file-open/open-file'

const baseFile = {
  path: '/missing/foo.go',
  name: 'foo.go',
  extension: 'go',
  size: 0,
  isDirectory: false,
}
const baseSource = { type: 'inapp' as const }
const baseCtx = {
  hostId: 'h1',
  cwd: '/sess/cwd',
  sourceWorkspaceId: 'w1',
  sessionCode: 's1',
}

describe('FileNotFoundPopup', () => {
  it('renders missing file path and the two primary CTAs in ask-expand mode', () => {
    const spec: PopupSpec = { mode: 'ask-expand', file: baseFile, source: baseSource, ctx: baseCtx }
    render(
      <FileNotFoundPopup
        spec={spec}
        sessionCwd="/sess/cwd"
        projectPath="/ws/project"
        onClose={vi.fn()}
        onOpenPath={vi.fn()}
        onSearchSessionCwd={vi.fn()}
        onSearchWorkspace={vi.fn()}
      />,
    )
    expect(screen.getByText('/missing/foo.go')).toBeInTheDocument()
    // Primary CTAs surface the root being searched.
    expect(screen.getByRole('button', { name: /session.*cwd|目前 session/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /workspace.*projectPath|workspace.*\/ws\/project/i })).toBeInTheDocument()
  })

  it('calls onSearchSessionCwd when the session-cwd CTA is clicked', () => {
    const onSearchSessionCwd = vi.fn()
    const spec: PopupSpec = { mode: 'ask-expand', file: baseFile, source: baseSource, ctx: baseCtx }
    render(
      <FileNotFoundPopup
        spec={spec}
        sessionCwd="/sess/cwd"
        projectPath="/ws/project"
        onClose={vi.fn()}
        onOpenPath={vi.fn()}
        onSearchSessionCwd={onSearchSessionCwd}
        onSearchWorkspace={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /session.*cwd|目前 session/i }))
    expect(onSearchSessionCwd).toHaveBeenCalledTimes(1)
  })

  it('disables session CTA with aria-disabled when sessionCode is missing (capability gap)', () => {
    const spec: PopupSpec = {
      mode: 'ask-expand',
      file: baseFile,
      source: baseSource,
      ctx: { ...baseCtx, sessionCode: undefined },
    }
    render(
      <FileNotFoundPopup
        spec={spec}
        sessionCwd={null}
        projectPath="/ws/project"
        onClose={vi.fn()}
        onOpenPath={vi.fn()}
        onSearchSessionCwd={vi.fn()}
        onSearchWorkspace={vi.fn()}
      />,
    )
    const btn = screen.getByRole('button', { name: /session.*cwd|目前 session/i })
    expect(btn.getAttribute('aria-disabled')).toBe('true')
    // Tooltip explains why
    expect(btn.getAttribute('title')).toMatch(/session/i)
  })

  it('disables workspace CTA when projectPath is missing', () => {
    const spec: PopupSpec = { mode: 'ask-expand', file: baseFile, source: baseSource, ctx: baseCtx }
    render(
      <FileNotFoundPopup
        spec={spec}
        sessionCwd="/sess/cwd"
        projectPath={null}
        onClose={vi.fn()}
        onOpenPath={vi.fn()}
        onSearchSessionCwd={vi.fn()}
        onSearchWorkspace={vi.fn()}
      />,
    )
    const btn = screen.getByRole('button', { name: /workspace/i })
    expect(btn.getAttribute('aria-disabled')).toBe('true')
  })

  it('layer1-multi mode renders candidate list and clicks call onOpenPath', () => {
    const spec: PopupSpec = {
      mode: 'layer1-multi',
      hits: ['/a/foo.go', '/b/foo.go'],
      file: baseFile,
      source: baseSource,
      ctx: baseCtx,
    }
    const onOpenPath = vi.fn()
    render(
      <FileNotFoundPopup
        spec={spec}
        sessionCwd="/sess/cwd"
        projectPath="/ws/project"
        onClose={vi.fn()}
        onOpenPath={onOpenPath}
        onSearchSessionCwd={vi.fn()}
        onSearchWorkspace={vi.fn()}
      />,
    )
    expect(screen.getByText('/a/foo.go')).toBeInTheDocument()
    expect(screen.getByText('/b/foo.go')).toBeInTheDocument()
    fireEvent.click(screen.getByText('/a/foo.go'))
    expect(onOpenPath).toHaveBeenCalledWith('/a/foo.go')
  })

  it('layer1-multi snapshot is independent of mid-flight prop changes (uses initial hits)', () => {
    // The component takes hits from spec.hits at render-time; subsequent
    // path-cache mutation shouldn't bleed into already-mounted popups.
    const spec: PopupSpec = {
      mode: 'layer1-multi',
      hits: ['/a/foo.go'],
      file: baseFile,
      source: baseSource,
      ctx: baseCtx,
    }
    const { rerender } = render(
      <FileNotFoundPopup
        spec={spec}
        sessionCwd="/sess/cwd"
        projectPath="/ws/project"
        onClose={vi.fn()}
        onOpenPath={vi.fn()}
        onSearchSessionCwd={vi.fn()}
        onSearchWorkspace={vi.fn()}
      />,
    )
    expect(screen.getByText('/a/foo.go')).toBeInTheDocument()
    // Same spec ref is preserved by callers; rerender with same spec → same snapshot.
    rerender(
      <FileNotFoundPopup
        spec={spec}
        sessionCwd="/sess/cwd"
        projectPath="/ws/project"
        onClose={vi.fn()}
        onOpenPath={vi.fn()}
        onSearchSessionCwd={vi.fn()}
        onSearchWorkspace={vi.fn()}
      />,
    )
    expect(screen.getByText('/a/foo.go')).toBeInTheDocument()
  })

  it('ESC key closes popup', () => {
    const onClose = vi.fn()
    const spec: PopupSpec = { mode: 'ask-expand', file: baseFile, source: baseSource, ctx: baseCtx }
    render(
      <FileNotFoundPopup
        spec={spec}
        sessionCwd="/sess/cwd"
        projectPath="/ws/project"
        onClose={onClose}
        onOpenPath={vi.fn()}
        onSearchSessionCwd={vi.fn()}
        onSearchWorkspace={vi.fn()}
      />,
    )
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('expanded mode renders both layer 2 and layer 3 sections', () => {
    const spec: PopupSpec = {
      mode: 'expanded',
      file: baseFile,
      source: baseSource,
      ctx: baseCtx,
      layer2Hits: [
        { path: '/sess/cwd/x/foo.go', modTime: '2026-04-27', sizeBytes: 1, root: '/sess/cwd' },
      ],
      layer3Hits: [
        { path: '/ws/project/y/foo.go', modTime: '2026-04-26', sizeBytes: 1, root: '/ws/project' },
      ],
    }
    const onOpenPath = vi.fn()
    render(
      <FileNotFoundPopup
        spec={spec}
        sessionCwd="/sess/cwd"
        projectPath="/ws/project"
        onClose={vi.fn()}
        onOpenPath={onOpenPath}
        onSearchSessionCwd={vi.fn()}
        onSearchWorkspace={vi.fn()}
      />,
    )
    expect(screen.getByText('/sess/cwd/x/foo.go')).toBeInTheDocument()
    expect(screen.getByText('/ws/project/y/foo.go')).toBeInTheDocument()
    fireEvent.click(screen.getByText('/sess/cwd/x/foo.go'))
    expect(onOpenPath).toHaveBeenCalledWith('/sess/cwd/x/foo.go')
  })
})
