import { vi } from 'vitest'
vi.mock('../../stores/useI18nStore', () => ({
  useI18nStore: Object.assign(vi.fn((sel: (s: { t: (k: string) => string }) => unknown) => sel({ t: (k: string) => k })), {
    getState: () => ({ t: (k: string) => k }),
  }),
}))

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { FilesWorkspaceSettingsSection } from './FilesWorkspaceSettingsSection'
import { useWorkspaceStore } from '../../features/workspace/store'

describe('FilesWorkspaceSettingsSection', () => {
  let wsId: string

  beforeEach(() => {
    cleanup()
    useWorkspaceStore.getState().reset()
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    wsId = ws.id
  })

  it('returns null when ctx scope is not workspace', () => {
    // @ts-expect-error — feeding wrong-scope ctx to verify runtime guard
    const { container } = render(<FilesWorkspaceSettingsSection ctx={{ scope: 'purdex' }} />)
    expect(container.innerHTML).toBe('')
  })

  it('renders empty value when no projectPath is set', () => {
    render(<FilesWorkspaceSettingsSection ctx={{ scope: 'workspace', workspaceId: wsId }} />)
    expect((screen.getByRole('textbox') as HTMLInputElement).value).toBe('')
  })

  it('renders existing projectPath', () => {
    useWorkspaceStore.getState().setModuleConfig(wsId, 'files', 'projectPath', '/home/user/proj')
    render(<FilesWorkspaceSettingsSection ctx={{ scope: 'workspace', workspaceId: wsId }} />)
    expect((screen.getByRole('textbox') as HTMLInputElement).value).toBe('/home/user/proj')
  })

  it('writes back to useWorkspaceStore.moduleConfig.files.projectPath on change', () => {
    render(<FilesWorkspaceSettingsSection ctx={{ scope: 'workspace', workspaceId: wsId }} />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '/new/path' } })
    const stored = useWorkspaceStore
      .getState()
      .workspaces.find((w) => w.id === wsId)?.moduleConfig?.['files']?.['projectPath']
    expect(stored).toBe('/new/path')
  })

  it('label uses i18n key settings.files.project_path.label', () => {
    render(<FilesWorkspaceSettingsSection ctx={{ scope: 'workspace', workspaceId: wsId }} />)
    expect(screen.getByLabelText('settings.files.project_path.label')).toBeInTheDocument()
  })
})
