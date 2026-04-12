// scripts/build-electron.mjs — Build Electron for both archs with per-arch icons
import { execSync } from 'child_process'
import { copyFileSync, renameSync, mkdirSync, existsSync } from 'fs'
import { resolve, dirname } from 'path'
import { fileURLToPath } from 'url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const iconDest = resolve(root, 'build/icon.icns')
const distDir = resolve(root, 'dist')

// Build each arch separately so icon.icns swap takes effect
for (const [arch, iconSrc] of [['x64', 'build/icon-x64.icns'], ['arm64', 'build/icon-arm64.icns']]) {
  copyFileSync(resolve(root, iconSrc), iconDest)
  console.log(`\n--- Building ${arch} (icon: ${iconSrc}) ---\n`)
  execSync(`npx electron-builder --mac --${arch} -c.directories.output=dist-${arch}`, { cwd: root, stdio: 'inherit' })

  // Move output to final dist/
  const srcApp = resolve(root, `dist-${arch}`, arch === 'x64' ? 'mac' : 'mac-arm64', 'Purdex.app')
  const destDir = resolve(distDir, arch === 'x64' ? 'mac' : 'mac-arm64')
  mkdirSync(destDir, { recursive: true })
  execSync(`rm -rf "${resolve(destDir, 'Purdex.app')}"`)
  renameSync(srcApp, resolve(destDir, 'Purdex.app'))
  execSync(`rm -rf "${resolve(root, `dist-${arch}`)}"`)
}
