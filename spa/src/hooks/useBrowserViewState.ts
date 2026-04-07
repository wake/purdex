import { useState, useEffect } from 'react'

export interface BrowserViewState {
  url: string
  title: string
  canGoBack: boolean
  canGoForward: boolean
  isLoading: boolean
}

const INITIAL_STATE: BrowserViewState = {
  url: '',
  title: '',
  canGoBack: false,
  canGoForward: false,
  isLoading: false,
}

export function useBrowserViewState(paneId: string): BrowserViewState {
  const [state, setState] = useState<BrowserViewState>(INITIAL_STATE)

  useEffect(() => {
    const api = window.electronAPI
    if (!api?.onBrowserViewStateUpdate) return

    const unsubscribe = api.onBrowserViewStateUpdate(
      (id: string, update: BrowserViewState) => {
        if (id === paneId) setState(update)
      },
    )

    return unsubscribe
  }, [paneId])

  return state
}
