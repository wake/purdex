import { ipcMain } from 'electron'
import { readFile, writeFile, stat, readdir, mkdir, rm, rename, realpath } from 'fs/promises'
import { resolve, isAbsolute } from 'path'
import { homedir } from 'os'

async function validatePath(path: string): Promise<string> {
  if (!isAbsolute(path)) throw new Error('Path must be absolute')
  // resolve() normalizes lexically; realpath() follows symlinks
  let resolved: string
  try {
    resolved = await realpath(resolve(path))
  } catch {
    // File may not exist yet (e.g., write to new path) — fall back to lexical resolve
    resolved = resolve(path)
  }
  const home = homedir()
  if (!resolved.startsWith(home + '/') && resolved !== home) {
    throw new Error('Access denied: path outside home directory')
  }
  return resolved
}

export function registerFsIpc(): void {
  ipcMain.handle('fs:read', async (_event, path: string) => {
    const resolved = await validatePath(path)
    const data = await readFile(resolved)
    return data
  })

  ipcMain.handle('fs:write', async (_event, path: string, content: Uint8Array) => {
    const resolved = await validatePath(path)
    await writeFile(resolved, content)
  })

  ipcMain.handle('fs:stat', async (_event, path: string) => {
    const resolved = await validatePath(path)
    const s = await stat(resolved)
    return { size: s.size, mtime: s.mtimeMs, isDirectory: s.isDirectory(), isFile: s.isFile() }
  })

  ipcMain.handle('fs:list', async (_event, path: string) => {
    const resolved = await validatePath(path)
    const entries = await readdir(resolved, { withFileTypes: true })
    return entries
      .filter((e) => !e.name.startsWith('.'))
      .map((e) => ({ name: e.name, isDir: e.isDirectory(), size: 0 }))
      .sort((a, b) => { if (a.isDir !== b.isDir) return a.isDir ? -1 : 1; return a.name.localeCompare(b.name) })
  })

  ipcMain.handle('fs:mkdir', async (_event, path: string, recursive: boolean) => {
    const resolved = await validatePath(path)
    await mkdir(resolved, { recursive })
  })

  ipcMain.handle('fs:delete', async (_event, path: string, recursive: boolean) => {
    const resolved = await validatePath(path)
    await rm(resolved, { recursive, force: recursive })
  })

  ipcMain.handle('fs:rename', async (_event, from: string, to: string) => {
    const resolvedFrom = await validatePath(from)
    const resolvedTo = await validatePath(to)
    await rename(resolvedFrom, resolvedTo)
  })
}
