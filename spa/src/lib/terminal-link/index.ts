export { terminalLinkRegistry, createRegistry } from './registry'
export { createXtermLinkProvider } from './xterm-provider'
export { registerBuiltinTerminalLinks } from './register'
export type { BuiltinTerminalLinksDeps } from './register'
export type {
  LinkToken,
  LinkMatcher,
  LinkOpener,
  LinkContext,
  LinkRange,
  TerminalLinkRegistry,
} from './types'
export {
  createFilePathMatcher,
  ABS_RE,
  REL_RE,
  BARE_RE,
} from './matchers/file-path'
export type { FilePathMatcherConfig } from './matchers/file-path'
