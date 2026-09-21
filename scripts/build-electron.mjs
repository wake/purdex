// scripts/build-electron.mjs — Build Electron for both archs with per-arch icons
import { execSync, spawnSync } from 'child_process'
import { copyFileSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from 'fs'
import { resolve, dirname } from 'path'
import { fileURLToPath, pathToFileURL } from 'url'

import { ENTITLEMENTS_PATH, assertLibraryValidationDisabled, signApp, verifyApp } from './mac-sign.mjs'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const iconDest = resolve(root, 'build/icon.icns')
const distDir = resolve(root, 'dist')
const pkg = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8'))
const appId = pkg.build?.appId ?? 'dev.wake.purdex'

/** Default `sign` collaborator: the real thing. */
function defaultSign(appPath, identity) {
  signApp(appPath, { identity, entitlements: ENTITLEMENTS_PATH })
}

/**
 * Signs `appPath` if it needs it, then asserts — on **every** path — that the
 * produced bundle will actually be able to launch (spec §5.4, goal G3).
 *
 * `sign` and `assert` are injectable so scripts/mac-sign_test.mjs can prove
 * that the assertion runs on both the freshly-signed path and the
 * already-signed early return, rather than taking the prose for it.
 */
export function signAndVerifyApp(appPath, { sign = defaultSign, assert = assertLibraryValidationDisabled } = {}) {
  if (process.platform !== 'darwin') return
  if (process.env.PDX_SKIP_MAC_SIGN === '1') {
    console.log(`Skipping macOS signing for ${appPath}`)
    return
  }

  const forcedIdentity = process.env.PDX_MAC_SIGN_IDENTITY
  if (!forcedIdentity && hasValidSignature(appPath)) {
    // electron-builder already signed this slice (arm64). Keep its signature,
    // but still refuse to ship it if it lacks the entitlements: if a future
    // electron-builder stops passing them, the build must break here rather
    // than at the user's launch time.
    console.log(`Keeping existing macOS signature for ${appPath}`)
    assert(appPath)
    return
  }

  const identity = forcedIdentity || '-'
  console.log(`Signing ${appPath} with ${identity === '-' ? 'ad-hoc identity' : identity}`)
  sign(appPath, identity)
  assert(appPath)
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

function build() {
  const originalIcon = readFileSync(iconDest)
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
}

// Only build when run as a script. Importing this module must stay
// side-effect-free so the signing tests can drive signAndVerifyApp().
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  build()
}
