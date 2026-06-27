/**
 * Maps a storage path to a Phosphor icon *name* (string, not a component),
 * mirroring the `getPaneIcon` → `ICON_MAP` pattern (pane-labels.ts /
 * useTabDisplay.ts). Every name returned here must exist as a key in the shared
 * `ICON_MAP` (tab-icon-map.tsx) so tabs and storage rows resolve icons the same
 * way.
 */

/** Extension (lowercase, no dot) → icon name. */
const EXTENSION_ICONS: Record<string, string> = {
  // Markup / docs
  md: 'FileMd',
  markdown: 'FileMd',
  txt: 'FileText',
  text: 'FileText',
  html: 'FileHtml',
  htm: 'FileHtml',
  // Code
  ts: 'FileTs',
  tsx: 'FileTsx',
  js: 'FileJs',
  mjs: 'FileJs',
  cjs: 'FileJs',
  jsx: 'FileJsx',
  vue: 'FileVue',
  py: 'FilePy',
  rs: 'FileRs',
  c: 'FileC',
  h: 'FileC',
  cpp: 'FileCpp',
  cc: 'FileCpp',
  cxx: 'FileCpp',
  hpp: 'FileCpp',
  css: 'FileCss',
  json: 'FileCode',
  yaml: 'FileCode',
  yml: 'FileCode',
  toml: 'FileCode',
  xml: 'FileCode',
  sql: 'FileSql',
  // Tabular
  csv: 'FileCsv',
  // Documents
  pdf: 'FilePdf',
  doc: 'FileDoc',
  docx: 'FileDoc',
  xls: 'FileXls',
  xlsx: 'FileXls',
  ppt: 'FilePpt',
  pptx: 'FilePpt',
  // Images
  png: 'FilePng',
  jpg: 'FileJpg',
  jpeg: 'FileJpg',
  svg: 'FileSvg',
  gif: 'FileImage',
  webp: 'FileImage',
  ico: 'FileImage',
  bmp: 'FileImage',
  // Audio / video
  mp3: 'FileAudio',
  wav: 'FileAudio',
  flac: 'FileAudio',
  ogg: 'FileAudio',
  m4a: 'FileAudio',
  mp4: 'FileVideo',
  mov: 'FileVideo',
  mkv: 'FileVideo',
  avi: 'FileVideo',
  // Archives
  zip: 'FileZip',
  tar: 'FileArchive',
  gz: 'FileArchive',
  tgz: 'FileArchive',
  rar: 'FileArchive',
  '7z': 'FileArchive',
}

export interface FileIconOptions {
  isDir?: boolean
  expanded?: boolean
}

/** Lowercase extension (no dot) of a path's basename, or '' if none. */
function extensionOf(path: string): string {
  const base = path.split('/').pop() ?? path
  const dot = base.lastIndexOf('.')
  // No dot, or a leading-dot dotfile with no further extension (e.g. ".gitignore").
  if (dot <= 0) return ''
  return base.slice(dot + 1).toLowerCase()
}

/**
 * Returns the icon name for a storage path. Directories resolve to
 * `FolderOpen` when `expanded`, otherwise `Folder`. Files resolve by
 * (case-insensitive) extension, falling back to `File` for unknown or
 * extensionless names.
 */
export function fileIconForPath(path: string, opts?: FileIconOptions): string {
  if (opts?.isDir) return opts.expanded ? 'FolderOpen' : 'Folder'
  const ext = extensionOf(path)
  return EXTENSION_ICONS[ext] ?? 'File'
}
