// spa/src/lib/platform.ts

export interface PlatformCapabilities {
  isElectron: boolean
  canTearOffTab: boolean
  canMergeWindow: boolean
  canBrowserPane: boolean
  canSystemTray: boolean
  canNotification: boolean
  devUpdateEnabled: boolean
}

export function getPlatformCapabilities(): PlatformCapabilities {
  const isElectron = !!window.electronAPI
  const devUpdateEnabled = isElectron && !!window.electronAPI?.getAppInfo
  return {
    isElectron,
    canTearOffTab: isElectron,
    canMergeWindow: isElectron,
    canBrowserPane: isElectron,
    canSystemTray: isElectron,
    canNotification: isElectron,
    devUpdateEnabled,
  }
}
