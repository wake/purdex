// spa/src/types/fs.ts

/** 標示檔案來自哪個 FS backend */
export type FileSource =
  | { type: 'daemon'; hostId: string }
  | { type: 'local' }
  | { type: 'inapp' }

/** File opener registry 使用的檔案資訊 */
export interface FileInfo {
  name: string
  path: string
  extension: string
  size: number
  isDirectory: boolean
}

/** stat() 回傳的檔案狀態 */
export interface FileStat {
  size: number
  mtime: number       // Unix timestamp ms
  isDirectory: boolean
  isFile: boolean
}

/** list() 回傳的目錄條目 */
export interface FileEntry {
  name: string
  isDir: boolean
  size: number
}
