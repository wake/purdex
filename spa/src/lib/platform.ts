// spa/src/lib/platform.ts

// The App (Electron) is the only shell that loads this SPA, so there is no
// "browser vs Electron" flag here. Both flags are real capability detection:
// the Mac App can load the SPA from the dev server while its Electron preload
// is OLDER than the SPA, so each one follows the preload method it needs.
export interface PlatformCapabilities {
  devUpdateEnabled: boolean
  hasLocalFilesystem: boolean
}

export function getPlatformCapabilities(): PlatformCapabilities {
  return {
    devUpdateEnabled: !!window.electronAPI?.getAppInfo,
    hasLocalFilesystem: !!window.electronAPI?.fs,
  }
}
