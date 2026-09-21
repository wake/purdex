// scripts/mac-sign_test.mjs — real-`codesign` tests for the macOS signing helpers.
//
// Spec: docs/specs/2026-09-22-x64-adhoc-entitlements-spec.md §6.
//
// These tests do NOT string-match the source. Every case below either calls a
// pure function or drives Apple's `codesign` against a throwaway `.app` built
// in a `mkdtemp` directory, which is removed again on the way out.
//
// Run with: pnpm run test:mac-sign   (i.e. `node scripts/mac-sign_test.mjs`)

import { execFileSync, spawnSync } from 'node:child_process'
import { chmodSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import {
  ENTITLEMENTS_PATH,
  assertLibraryValidationDisabled,
  buildSignArgs,
  hasHardenedRuntime,
  readEntitlements,
} from './mac-sign.mjs'

const scriptsDir = dirname(fileURLToPath(import.meta.url))
const root = resolve(scriptsDir, '..')
const appId = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8')).build?.appId ?? 'dev.wake.purdex'

const LIB_VAL_KEY = 'com.apple.security.cs.disable-library-validation'

// ---------------------------------------------------------------- tiny harness

const cases = []
const test = (name, fn) => cases.push({ name, fn })

function assertTrue(cond, message) {
  if (!cond) throw new Error(message)
}

function assertEqual(actual, expected, message) {
  if (actual !== expected) {
    throw new Error(`${message}\n  expected: ${JSON.stringify(expected)}\n  actual:   ${JSON.stringify(actual)}`)
  }
}

function assertThrows(fn, message) {
  let threw = false
  try {
    fn()
  } catch {
    threw = true
  }
  if (!threw) throw new Error(message)
}

// ------------------------------------------------------------- bundle fixtures

function infoPlist({ executable, identifier, name }) {
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>CFBundleExecutable</key>
    <string>${executable}</string>
    <key>CFBundleIdentifier</key>
    <string>${identifier}</string>
    <key>CFBundleName</key>
    <string>${name}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleInfoDictionaryVersion</key>
    <string>6.0</string>
    <key>CFBundleShortVersionString</key>
    <string>1.0</string>
    <key>CFBundleVersion</key>
    <string>1</string>
  </dict>
</plist>
`
}

/** Creates `<dir>/<name>.app` with a real Mach-O executable inside. */
function makeBundle(dir, { name, identifier }) {
  const app = join(dir, `${name}.app`)
  const macos = join(app, 'Contents', 'MacOS')
  mkdirSync(macos, { recursive: true })
  writeFileSync(join(app, 'Contents', 'Info.plist'), infoPlist({ executable: name, identifier, name }))
  const exe = join(macos, name)
  // A real signable Mach-O; ad-hoc re-signing a copy of a system tool is allowed.
  copyFileSync('/bin/echo', exe)
  chmodSync(exe, 0o755)
  return app
}

/** Top-level `.app` plus a nested `Contents/Frameworks/T Helper.app`. */
function makeAppWithHelper(dir, { appIdentifier = appId, helperIdentifier = `${appId}.helper.GPU` } = {}) {
  const app = makeBundle(dir, { name: 'T', identifier: appIdentifier })
  const frameworks = join(app, 'Contents', 'Frameworks')
  mkdirSync(frameworks, { recursive: true })
  const helper = makeBundle(frameworks, { name: 'T Helper', identifier: helperIdentifier })
  return { app, helper }
}

function codesign(args) {
  return execFileSync('codesign', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
}

function identifierOf(bundlePath) {
  const r = spawnSync('codesign', ['-dv', '--verbose=4', bundlePath], { encoding: 'utf8' })
  const match = `${r.stdout}${r.stderr}`.match(/^Identifier=(.*)$/m)
  return match ? match[1].trim() : null
}

function withTempDir(fn) {
  const dir = mkdtempSync(join(tmpdir(), 'pdx-mac-sign-'))
  try {
    return fn(dir)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}

// ------------------------------------------------------------------- case 1

test('1. buildSignArgs carries --entitlements / --options runtime and drops --identifier', () => {
  const appPath = '/tmp/does-not-need-to-exist/Purdex.app'
  const args = buildSignArgs({ appPath, identity: '-', entitlements: ENTITLEMENTS_PATH })

  assertTrue(Array.isArray(args), 'buildSignArgs must return an array')

  const entIdx = args.indexOf('--entitlements')
  assertTrue(entIdx !== -1, `argv must contain --entitlements; got ${JSON.stringify(args)}`)
  assertEqual(args[entIdx + 1], ENTITLEMENTS_PATH, '--entitlements must be followed by the plist path')

  const optIdx = args.indexOf('--options')
  assertTrue(optIdx !== -1, '--options must be present')
  assertEqual(args[optIdx + 1], 'runtime', '--options must be followed by runtime')

  assertTrue(!args.includes('--identifier'), `--identifier must be absent (spec §5.3); got ${JSON.stringify(args)}`)

  assertEqual(args[args.length - 1], appPath, 'the bundle path must be the final argument')
  assertTrue(args.includes('--force'), '--force must be present')
  assertTrue(args.includes('--deep'), '--deep must be present')
  assertTrue(args.includes('--timestamp=none'), 'the ad-hoc identity must sign with --timestamp=none')

  const signIdx = args.indexOf('--sign')
  assertTrue(signIdx !== -1, '--sign must be present')
  assertEqual(args[signIdx + 1], '-', 'the ad-hoc identity must be passed through verbatim')

  // A named identity must not get --timestamp=none.
  const named = buildSignArgs({ appPath, identity: 'Developer ID Application: Someone', entitlements: ENTITLEMENTS_PATH })
  assertTrue(!named.includes('--timestamp=none'), 'a real identity must be timestamped')

  // The shipped default points at the repo's plist.
  assertEqual(ENTITLEMENTS_PATH, resolve(root, 'electron/entitlements.mac.plist'), 'ENTITLEMENTS_PATH must be the repo plist')
  assertTrue(readFileSync(ENTITLEMENTS_PATH, 'utf8').includes(LIB_VAL_KEY), 'the plist must carry disable-library-validation')
})

// ------------------------------------------------------------------- case 2

test('2. real codesign: entitlements reach the app and its nested helper; removing them re-arms the guard', () => {
  withTempDir((dir) => {
    // --- signed the way the build signs it -------------------------------
    const deepDir = join(dir, 'deep')
    mkdirSync(deepDir)
    const { app, helper } = makeAppWithHelper(deepDir)
    codesign(buildSignArgs({ appPath: app, identity: '-', entitlements: ENTITLEMENTS_PATH }))

    const appEnts = readEntitlements(app)
    assertTrue(appEnts.includes(LIB_VAL_KEY), `app entitlements must contain ${LIB_VAL_KEY}; got: ${appEnts || '(empty)'}`)

    const helperEnts = readEntitlements(helper)
    assertTrue(
      helperEnts.includes(LIB_VAL_KEY),
      `nested helper entitlements must contain ${LIB_VAL_KEY}; got: ${helperEnts || '(empty)'}`,
    )

    assertLibraryValidationDisabled(app)
    assertLibraryValidationDisabled(helper)
    assertTrue(hasHardenedRuntime(app), 'the app must carry Hardened Runtime')
    assertTrue(hasHardenedRuntime(helper), 'the nested helper must carry Hardened Runtime')

    // --- identifiers: dropping --identifier keeps nested ids intact -------
    assertEqual(identifierOf(app), appId, 'the top-level identifier still comes from Info.plist without --identifier')
    assertEqual(
      identifierOf(helper),
      `${appId}.helper.GPU`,
      'the nested helper must keep its own identifier, not be flattened onto the app id',
    )

    // --- control: without --deep the helper is left uncovered -------------
    const shallowDir = join(dir, 'shallow')
    mkdirSync(shallowDir)
    const shallow = makeAppWithHelper(shallowDir)
    // Sign the helper on its own first, hardened but unentitled, so that the
    // assertions below measure "--deep did not reach it" rather than
    // "the helper is unsigned".
    codesign(['--force', '--options', 'runtime', '--sign', '-', '--timestamp=none', shallow.helper])
    codesign(
      buildSignArgs({ appPath: shallow.app, identity: '-', entitlements: ENTITLEMENTS_PATH }).filter((a) => a !== '--deep'),
    )
    assertTrue(readEntitlements(shallow.app).includes(LIB_VAL_KEY), 'the top-level app is still entitled without --deep')
    assertEqual(readEntitlements(shallow.helper), '', 'without --deep the nested helper receives no entitlements')
    assertThrows(
      () => assertLibraryValidationDisabled(shallow.helper),
      'a hardened, unentitled nested helper must fail assertLibraryValidationDisabled() — otherwise dropping --deep would ship silently',
    )

    // --- the defect itself: re-sign without --entitlements -> guard throws -
    const stripped = buildSignArgs({ appPath: app, identity: '-', entitlements: ENTITLEMENTS_PATH })
    const idx = stripped.indexOf('--entitlements')
    stripped.splice(idx, 2)
    codesign(stripped)
    assertEqual(readEntitlements(app), '', 'a --force re-sign without --entitlements clears the previous entitlements')
    assertThrows(
      () => assertLibraryValidationDisabled(app),
      'a hardened bundle with no entitlements must throw — this is the exact configuration that crashed on Intel',
    )
  })
})

// ------------------------------------------------------------------- case 3

test('3. the guard keys off Hardened Runtime, not merely off the presence of entitlements', () => {
  withTempDir((dir) => {
    const app = makeBundle(dir, { name: 'T', identifier: `${appId}.plain` })
    // Ad-hoc, no --options runtime, no entitlements.
    codesign(['--force', '--sign', '-', '--timestamp=none', app])

    assertEqual(hasHardenedRuntime(app), false, 'a bundle signed without --options runtime must not report Hardened Runtime')
    assertEqual(readEntitlements(app), '', 'this bundle has no entitlements')
    // Must NOT throw: without Hardened Runtime there is no Library Validation
    // to disable, so the guard has nothing to complain about.
    assertLibraryValidationDisabled(app)
  })
})

// ------------------------------------------------------------------- case 4

const DRIVER = `
const [, , moduleUrl, unsignedApp, signedApp] = process.argv
const mod = await import(moduleUrl)
if (typeof mod.signAndVerifyApp !== 'function') {
  throw new Error('build-electron.mjs does not export signAndVerifyApp')
}
const calls = []
const sign = (p) => { calls.push('sign:' + p) }
const assert = (p) => { calls.push('assert:' + p) }

await mod.signAndVerifyApp(unsignedApp, { sign, assert })
const fresh = calls.splice(0, calls.length)
await mod.signAndVerifyApp(signedApp, { sign, assert })
const kept = calls.splice(0, calls.length)

console.log('__RESULT__' + JSON.stringify({ fresh, kept }))
`

test('4. signAndVerifyApp asserts on BOTH paths: freshly signed and existing-signature early return', () => {
  withTempDir((dir) => {
    const unsignedDir = join(dir, 'unsigned')
    const signedDir = join(dir, 'signed')
    mkdirSync(unsignedDir)
    mkdirSync(signedDir)

    // Path A: nothing signed it yet -> hasValidSignature() is false.
    const unsignedApp = makeBundle(unsignedDir, { name: 'T', identifier: appId })
    // Path B: already carries a valid signature with the expected Identifier,
    // exactly like the arm64 bundle electron-builder hands us.
    const signedApp = makeBundle(signedDir, { name: 'T', identifier: appId })
    codesign(buildSignArgs({ appPath: signedApp, identity: '-', entitlements: ENTITLEMENTS_PATH }))

    const driverPath = join(dir, 'driver.mjs')
    writeFileSync(driverPath, DRIVER)

    const result = spawnSync(
      process.execPath,
      [driverPath, resolve(scriptsDir, 'build-electron.mjs'), unsignedApp, signedApp],
      {
        encoding: 'utf8',
        timeout: 60_000,
        killSignal: 'SIGKILL',
        cwd: root,
        env: {
          // Deliberately minimal: `codesign` lives in /usr/bin, `npx` does not.
          // If build-electron.mjs ever regains a top-level build side effect,
          // this child dies immediately instead of launching electron-builder.
          PATH: '/usr/bin:/bin',
          HOME: process.env.HOME ?? '',
          TMPDIR: process.env.TMPDIR ?? '/tmp',
        },
      },
    )

    const output = `${result.stdout ?? ''}${result.stderr ?? ''}`
    assertEqual(result.status, 0, `the signAndVerifyApp driver must exit 0; output was:\n${output}`)

    const marker = (result.stdout ?? '').match(/^__RESULT__(.*)$/m)
    assertTrue(marker !== null, `driver produced no __RESULT__ line; output was:\n${output}`)
    const { fresh, kept } = JSON.parse(marker[1])

    assertEqual(
      JSON.stringify(fresh),
      JSON.stringify([`sign:${unsignedApp}`, `assert:${unsignedApp}`]),
      'the fresh-signing path must sign and then assert',
    )
    assertEqual(
      JSON.stringify(kept),
      JSON.stringify([`assert:${signedApp}`]),
      'the hasValidSignature() early-return path must still assert (goal G3) and must not re-sign',
    )
  })
})

// ---------------------------------------------------------------------- run

if (process.platform !== 'darwin') {
  console.log('SKIP: scripts/mac-sign_test.mjs requires macOS `codesign` (platform is ' + process.platform + ')')
  process.exit(0)
}

let failures = 0
for (const { name, fn } of cases) {
  try {
    fn()
    console.log(`ok   ${name}`)
  } catch (err) {
    failures += 1
    console.log(`FAIL ${name}`)
    console.log(String(err?.stack ?? err).split('\n').map((l) => `     ${l}`).join('\n'))
  }
}

console.log(`\n${cases.length - failures}/${cases.length} passed`)
process.exit(failures === 0 ? 0 : 1)
