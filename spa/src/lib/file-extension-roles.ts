/**
 * file-extension-roles — the single source of truth for mapping a file
 * extension to its open "role" (image preview / pdf preview / forced download /
 * text editor).
 *
 * This is a **leaf lib** (Phase 1c decision 2 / codex R1 F2): it imports nothing
 * from the app. Both the module-registration layer
 * (`register-modules/editor-module.tsx`, which wires the `image-preview` /
 * `pdf-viewer` / `monaco-editor` file-openers) and `lib/open-in-app-file.ts`
 * (which decides binary open-disposition) consume the same sets from here. Were
 * the sets to live in `editor-module.tsx`, `open-in-app-file.ts` reading them
 * back would form a `lib → module-registration → StoragePane → lib` cycle, so
 * the SOT must sit at this leaf level.
 */

/** Extensions that open in the image-preview pane. */
export const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico'])

/** Extensions that open in the pdf-preview pane. */
export const PDF_EXTS = new Set(['pdf'])

/**
 * Non-previewable binary / office / archive extensions that are downloaded
 * rather than mounted in a pane. Deliberately excludes every text/code
 * extension — those must fall through to the monaco editor. Mounting e.g. a
 * `.docx` as text produced garbled output, which this set fixes by routing it
 * to a download instead.
 */
export const DOWNLOAD_EXTS = new Set([
  // office documents
  'doc',
  'docx',
  'xls',
  'xlsx',
  'ppt',
  'pptx',
  // archives
  'zip',
  'rar',
  '7z',
  'tar',
  'gz',
  'bz2',
  'xz',
  // opaque binaries / installers
  'bin',
  'exe',
  'dmg',
  'iso',
  'so',
  'dll',
  'wasm',
])

export type FileRole = 'image' | 'pdf' | 'download' | 'text'

/**
 * The open role for an extension. Image/pdf hit their dedicated preview panes,
 * download covers the non-previewable binaries above, and everything else
 * (text, code, unknown, ext-less) is treated as text → monaco editor.
 */
export function roleForExtension(ext: string): FileRole {
  const e = ext.toLowerCase()
  if (IMAGE_EXTS.has(e)) return 'image'
  if (PDF_EXTS.has(e)) return 'pdf'
  if (DOWNLOAD_EXTS.has(e)) return 'download'
  return 'text'
}
