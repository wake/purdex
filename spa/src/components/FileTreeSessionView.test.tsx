import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { FileTreeSessionView } from './FileTreeSessionView'
import { useI18nStore } from '../stores/useI18nStore'

beforeEach(() => {
  useI18nStore.getState().setLocale('en')
})

describe('FileTreeSessionView', () => {
  describe('locale-aware not-implemented text (#1337)', () => {
    it('shows the English not-implemented text for the en locale', () => {
      render(<FileTreeSessionView isActive={true} />)
      expect(screen.getByText('Session file tree not yet implemented (needs daemon cwd API)')).toBeInTheDocument()
      expect(screen.queryByText('Session file tree 尚未實作（需 daemon cwd API）')).not.toBeInTheDocument()
    })

    it('shows the zh-TW not-implemented text for the zh-TW locale', () => {
      useI18nStore.getState().setLocale('zh-TW')
      render(<FileTreeSessionView isActive={true} />)
      expect(screen.getByText('Session file tree 尚未實作（需 daemon cwd API）')).toBeInTheDocument()
    })
  })
})
