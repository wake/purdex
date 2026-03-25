// spa/src/lib/platform.ts

export interface PlatformCapabilities {
  canTearOffTab: boolean
  canMergeWindow: boolean
  canBrowserPane: boolean
  canSystemTray: boolean
}

export function getPlatformCapabilities(): PlatformCapabilities {
  const isElectron = !!window.electronAPI
  return {
    canTearOffTab: isElectron,
    canMergeWindow: isElectron,
    canBrowserPane: isElectron,
    canSystemTray: isElectron,
  }
}
