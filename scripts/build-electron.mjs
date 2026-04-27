// scripts/build-electron.mjs — Build Electron for both archs with per-arch icons
import { execFileSync, execSync, spawnSync } from 'child_process'
import { copyFileSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from 'fs'
import { resolve, dirname } from 'path'
import { fileURLToPath } from 'url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const iconDest = resolve(root, 'build/icon.icns')
const distDir = resolve(root, 'dist')
const pkg = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8'))
const appId = pkg.build?.appId ?? 'dev.wake.purdex'
const originalIcon = readFileSync(iconDest)

function signAndVerifyApp(appPath) {
  if (process.platform !== 'darwin') return
  if (process.env.PDX_SKIP_MAC_SIGN === '1') {
    console.log(`Skipping macOS signing for ${appPath}`)
    return
  }

  const forcedIdentity = process.env.PDX_MAC_SIGN_IDENTITY
  if (!forcedIdentity && hasValidSignature(appPath)) {
    console.log(`Keeping existing macOS signature for ${appPath}`)
    return
  }

  const identity = forcedIdentity || '-'
  const signArgs = [
    '--force',
    '--deep',
    '--options', 'runtime',
    '--identifier', appId,
    '--sign', identity,
  ]
  if (identity === '-') signArgs.push('--timestamp=none')
  signArgs.push(appPath)

  console.log(`Signing ${appPath} with ${identity === '-' ? 'ad-hoc identity' : identity}`)
  execFileSync('codesign', signArgs, { stdio: 'inherit' })
  verifyApp(appPath)
}

function hasValidSignature(appPath) {
  try {
    verifyApp(appPath)
    const result = spawnSync('codesign', ['-dv', '--verbose=4', appPath], { encoding: 'utf8' })
    if (result.status !== 0) return false
    const output = `${result.stdout}${result.stderr}`
    return output.includes(`Identifier=${appId}`)
  } catch {
    return false
  }
}

function verifyApp(appPath) {
  execFileSync('codesign', ['--verify', '--deep', '--strict', '--verbose=4', appPath], { stdio: 'inherit' })
}

try {
  // Build each arch separately so icon.icns swap takes effect
  for (const [arch, iconSrc] of [['x64', 'build/icon-x64.icns'], ['arm64', 'build/icon-arm64.icns']]) {
    copyFileSync(resolve(root, iconSrc), iconDest)
    console.log(`\n--- Building ${arch} (icon: ${iconSrc}) ---\n`)
    execSync(`npx electron-builder --mac --${arch} -c.directories.output=dist-${arch}`, { cwd: root, stdio: 'inherit' })

    // Move output to final dist/
    const srcApp = resolve(root, `dist-${arch}`, arch === 'x64' ? 'mac' : 'mac-arm64', 'Purdex.app')
    const destDir = resolve(distDir, arch === 'x64' ? 'mac' : 'mac-arm64')
    const destApp = resolve(destDir, 'Purdex.app')
    mkdirSync(destDir, { recursive: true })
    rmSync(destApp, { recursive: true, force: true })
    renameSync(srcApp, destApp)
    signAndVerifyApp(destApp)
    rmSync(resolve(root, `dist-${arch}`), { recursive: true, force: true })
  }
} finally {
  writeFileSync(iconDest, originalIcon)
}
