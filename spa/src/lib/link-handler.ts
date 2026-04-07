export interface LinkHandlerDeps {
  isElectron: boolean
  openBrowserTab: (url: string) => void
  openMiniWindow: (url: string) => void
}

export function createLinkHandler(deps: LinkHandlerDeps) {
  return (event: MouseEvent, uri: string): void => {
    if (deps.isElectron) {
      if (event.shiftKey) {
        deps.openMiniWindow(uri)
      } else {
        deps.openBrowserTab(uri)
      }
    } else {
      window.open(uri, '_blank')
    }
  }
}
