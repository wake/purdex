import { Component, type ReactNode } from 'react'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  children: ReactNode
}

interface State {
  hasError: boolean
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, error: null }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  render() {
    if (!this.state.hasError) return this.props.children

    const t = useI18nStore.getState().t
    return (
      <div className="h-screen flex items-center justify-center bg-surface-primary text-text-primary">
        <div className="text-center max-w-md p-6">
          <h1 className="text-lg font-semibold mb-2">{t('error.boundary.title')}</h1>
          <p className="text-sm text-text-muted mb-4">
            {this.state.error?.message ?? t('error.boundary.message')}
          </p>
          <button
            onClick={() => window.location.reload()}
            className="px-4 py-2 rounded text-sm bg-accent text-white cursor-pointer"
          >
            {t('error.boundary.reload')}
          </button>
        </div>
      </div>
    )
  }
}
