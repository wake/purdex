// spa/src/lib/browser-shortcuts.ts
import { registerTabShortcuts } from './tab-shortcut-registry'

registerTabShortcuts('browser', {
  'go-back': (_tab, pane) => window.electronAPI?.browserViewGoBack(pane.id),
  'go-forward': (_tab, pane) => window.electronAPI?.browserViewGoForward(pane.id),
  'reload': (_tab, pane) => window.electronAPI?.browserViewReload(pane.id),
  'focus-url': () => document.dispatchEvent(new CustomEvent('browser:focus-url')),
  'print': (_tab, pane) => window.electronAPI?.browserViewPrint(pane.id),
})
