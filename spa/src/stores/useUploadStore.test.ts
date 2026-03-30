import { describe, it, expect, beforeEach } from 'vitest'
import { useUploadStore } from './useUploadStore'

beforeEach(() => {
  useUploadStore.setState({ sessions: {} })
})

describe('useUploadStore', () => {
  it('startUpload initialises session state', () => {
    useUploadStore.getState().startUpload('dev', 3, 'a.png')
    const s = useUploadStore.getState().sessions['dev']
    expect(s.status).toBe('uploading')
    expect(s.total).toBe(3)
    expect(s.completed).toBe(0)
    expect(s.failed).toBe(0)
    expect(s.currentFile).toBe('a.png')
  })

  it('fileCompleted increments count and sets done when all complete', () => {
    useUploadStore.getState().startUpload('dev', 2, 'a.png')
    useUploadStore.getState().fileCompleted('dev')
    expect(useUploadStore.getState().sessions['dev'].completed).toBe(1)
    expect(useUploadStore.getState().sessions['dev'].status).toBe('uploading')

    useUploadStore.getState().fileCompleted('dev')
    expect(useUploadStore.getState().sessions['dev'].completed).toBe(2)
    expect(useUploadStore.getState().sessions['dev'].status).toBe('done')
  })

  it('fileFailed increments failed count and sets error status when all done', () => {
    useUploadStore.getState().startUpload('dev', 1, 'a.png')
    useUploadStore.getState().fileFailed('dev', 'a.png')
    const s = useUploadStore.getState().sessions['dev']
    expect(s.failed).toBe(1)
    expect(s.error).toBe('a.png')
    expect(s.status).toBe('error')
  })

  it('partial success: some completed some failed', () => {
    useUploadStore.getState().startUpload('dev', 3, 'a.png')
    useUploadStore.getState().fileCompleted('dev')
    useUploadStore.getState().fileFailed('dev', 'b.png')
    useUploadStore.getState().fileCompleted('dev')
    const s = useUploadStore.getState().sessions['dev']
    expect(s.completed).toBe(2)
    expect(s.failed).toBe(1)
    expect(s.status).toBe('error')
  })

  it('nextFile updates currentFile', () => {
    useUploadStore.getState().startUpload('dev', 2, 'a.png')
    useUploadStore.getState().nextFile('dev', 'b.png')
    expect(useUploadStore.getState().sessions['dev'].currentFile).toBe('b.png')
  })

  it('dismiss clears session state', () => {
    useUploadStore.getState().startUpload('dev', 1, 'a.png')
    useUploadStore.getState().dismiss('dev')
    expect(useUploadStore.getState().sessions['dev']).toBeUndefined()
  })
})
