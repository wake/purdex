import { describe, it, expect, beforeEach } from 'vitest'
import { useUploadStore } from './useUploadStore'

const H = 'test-host'

beforeEach(() => {
  useUploadStore.setState({ sessions: {} })
})

describe('useUploadStore', () => {
  it('startUpload initialises session state', () => {
    useUploadStore.getState().startUpload(H, 'dev', 3, 'a.png')
    const s = useUploadStore.getState().sessions[`${H}:dev`]
    expect(s.status).toBe('uploading')
    expect(s.total).toBe(3)
    expect(s.completed).toBe(0)
    expect(s.failed).toBe(0)
    expect(s.currentFile).toBe('a.png')
  })

  it('fileCompleted increments count and sets typing when all complete', () => {
    useUploadStore.getState().startUpload(H, 'dev', 2, 'a.png')
    useUploadStore.getState().fileCompleted(H, 'dev')
    expect(useUploadStore.getState().sessions[`${H}:dev`].completed).toBe(1)
    expect(useUploadStore.getState().sessions[`${H}:dev`].status).toBe('uploading')

    useUploadStore.getState().fileCompleted(H, 'dev')
    expect(useUploadStore.getState().sessions[`${H}:dev`].completed).toBe(2)
    expect(useUploadStore.getState().sessions[`${H}:dev`].status).toBe('typing')
  })

  it('setDone transitions typing to done', () => {
    useUploadStore.getState().startUpload(H, 'dev', 1, 'a.png')
    useUploadStore.getState().fileCompleted(H, 'dev')
    expect(useUploadStore.getState().sessions[`${H}:dev`].status).toBe('typing')
    useUploadStore.getState().setDone(H, 'dev')
    expect(useUploadStore.getState().sessions[`${H}:dev`].status).toBe('done')
  })

  it('fileFailed increments failed count and sets error status when all done', () => {
    useUploadStore.getState().startUpload(H, 'dev', 1, 'a.png')
    useUploadStore.getState().fileFailed(H, 'dev', 'a.png')
    const s = useUploadStore.getState().sessions[`${H}:dev`]
    expect(s.failed).toBe(1)
    expect(s.error).toBe('a.png')
    expect(s.status).toBe('error')
  })

  it('partial success: some completed some failed', () => {
    useUploadStore.getState().startUpload(H, 'dev', 3, 'a.png')
    useUploadStore.getState().fileCompleted(H, 'dev')
    useUploadStore.getState().fileFailed(H, 'dev', 'b.png')
    useUploadStore.getState().fileCompleted(H, 'dev')
    const s = useUploadStore.getState().sessions[`${H}:dev`]
    expect(s.completed).toBe(2)
    expect(s.failed).toBe(1)
    expect(s.status).toBe('error')
  })

  it('nextFile updates currentFile', () => {
    useUploadStore.getState().startUpload(H, 'dev', 2, 'a.png')
    useUploadStore.getState().nextFile(H, 'dev', 'b.png')
    expect(useUploadStore.getState().sessions[`${H}:dev`].currentFile).toBe('b.png')
  })

  it('dismiss clears done/error state', () => {
    useUploadStore.getState().startUpload(H, 'dev', 1, 'a.png')
    useUploadStore.getState().fileCompleted(H, 'dev') // status → typing
    useUploadStore.getState().setDone(H, 'dev') // status → done
    useUploadStore.getState().dismiss(H, 'dev')
    expect(useUploadStore.getState().sessions[`${H}:dev`]).toBeUndefined()
  })

  it('dismiss does not clear uploading state', () => {
    useUploadStore.getState().startUpload(H, 'dev', 3, 'a.png')
    useUploadStore.getState().dismiss(H, 'dev')
    expect(useUploadStore.getState().sessions[`${H}:dev`]).toBeDefined()
    expect(useUploadStore.getState().sessions[`${H}:dev`].status).toBe('uploading')
  })

  it('dismiss does not clear typing state', () => {
    useUploadStore.getState().startUpload(H, 'dev', 1, 'a.png')
    useUploadStore.getState().fileCompleted(H, 'dev')
    expect(useUploadStore.getState().sessions[`${H}:dev`].status).toBe('typing')
    useUploadStore.getState().dismiss(H, 'dev')
    expect(useUploadStore.getState().sessions[`${H}:dev`]).toBeDefined()
    expect(useUploadStore.getState().sessions[`${H}:dev`].status).toBe('typing')
  })
})
