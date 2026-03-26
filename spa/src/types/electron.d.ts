// spa/src/types/electron.d.ts
interface Window {
  electronAPI?: {
    tearOffTab: (tabJson: string) => Promise<void>
    mergeTab: (tabJson: string, targetWindowId: string) => Promise<void>
    openBrowserView: (url: string, paneId: string) => Promise<void>
    closeBrowserView: (paneId: string) => Promise<void>
    navigateBrowserView: (paneId: string, url: string) => Promise<void>
    onTabReceived: (callback: (tabJson: string) => void) => () => void
  }
}
