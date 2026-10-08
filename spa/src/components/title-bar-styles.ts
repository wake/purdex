// spa/src/components/title-bar-styles.ts — the title bar's button styles, shared by the layout buttons (TitleBar.tsx)
// and the 無人值守模式 toggle (UnattendedButton.tsx), which TitleBar renders (a module of its own: no import cycle).
export const BUTTON = 'p-1 rounded cursor-pointer disabled:opacity-40 disabled:pointer-events-none'
export const PRESSED = 'text-accent-base bg-accent-base/10'
export const IDLE = 'text-text-secondary hover:text-text-primary hover:bg-surface-hover'
