// @vitest-environment node
//
// Guard (host-ownership H2 plan §0.14): no screen reads a host's look straight
// off `HostConfig` — it goes through `lib/host-look.ts`. A grep cannot tell
// `host.icon` from `file.icon`, so this runs the TypeScript checker over the
// app program (`tsconfig.app.json`) and counts every READ of a checked field
// on an expression whose type is — or has in a union / intersection — the
// `HostConfig` interface of `stores/useHostStore.ts` (a type alias of it is the
// same symbol).
//
// Counted as a read:
//   - `x.f`, `x?.f`, `x['f']` (string-literal element access);
//   - object-binding destructuring of `f` (`const { f } = host`, nested too),
//     including a literal computed key (`{ ['f']: v }`, `` { [`f`]: v } ``);
//   - parameter destructuring `({ f }: HostConfig)`, same key forms.
// Not a read: the left side of an assignment (`next.icon = x`, `x.f += …`),
// `delete x.f`, an object-literal property (`{ icon: x }`). Writes are the
// writer tests' job.
//
// KNOWN FALSE NEGATIVES (the fixture half pins each one as "not flagged", so a
// change of the detector is visible):
//   - computed access with a non-literal key: `h[f]` (e.g. a loop over the look
//     field names), and a destructuring key that is not a literal
//     (`const k = 'icon'; const { [k]: v } = h` — even when `k` is a constant);
//   - a value first narrowed / copied into another type: `(h as { icon?: string }).icon`,
//     `Pick<HostConfig, 'icon'>`, `Partial<HostConfig>`, a spread `({ ...h }).icon`;
//   - destructuring ASSIGNMENT (`({ icon } = h)`), reads in `.d.ts` files or
//     `any`-typed code;
//   - test files (excluded by design: `*.test.*`, `__tests__/`, `test-setup.ts`,
//     `test-utils*`).
//
// Every hit also carries `decl`, a lexical path that is unique within its file:
// the chain of enclosing KEY SEGMENTS, outermost first, joined with `.`
// (`useHostStore.persist(arg0).setHostIcon.set(arg0)`, `A.m` vs `B.m`);
// `<module>` when there is none. A key segment is
//   - a function / class declaration (anonymous: `default` when exported as
//     default, else `<anonymous>`), a method / accessor / constructor, an
//     object-literal property or a class field (a non-literal computed name is
//     `<computed>`);
//   - a variable declaration with a plain name whose initializer is a function
//     or class, or any such variable at module level;
//   - a function / class expression that is not the initializer of one of the
//     above, named by its own name or else by where it is bound: `default`
//     (`export default`), `callee(argN)` (a call / `new` argument), the JSX
//     attribute name, or `<anonymous>`.
// When siblings under the same parent segment share a name, each gets `#n`,
// its 1-based order among them. Nothing positional (line, offset) enters the
// key, so an unrelated edit leaves every key alone; adding a same-named sibling
// renumbers the `#n`s — a loud failure, never a false green.
//
// The allowlist is file → decl → field → EXACT count, and the repo test demands
// the hit multiset equal it: a new read, a removed read, or a read moved into
// another declaration of the same file (same file-wide count) all fail and
// print `file:line decl field`.
//
// This guard is a TRIPWIRE, not an enforcement boundary: the `ALLOWLIST`
// constant and the verbatim copy pinned in the "pinned" test live in this one
// file, so one PR can widen both. What it buys is that any widening is an
// explicit, reviewable diff to this file. Permanently only `stores/useHostStore.ts`,
// `lib/host-color.ts` and `lib/host-look.ts` may appear (the spec §4.2
// exception), plus `lib/host-look-migration.ts` (spec §4.4: the first-run
// migration reads `HostConfig` looks as a source; H2c-2); every other allowlisted file is TEMPORARY, named in
// `TEMPORARY_FILES` with the PR that removes it, and a test asserts the
// allowlisted files are a subset of those four plus exactly that list.
// H2a checked the colour / icon fields; H2b-1 adds `name`; H2b-2 moves the
// last name surfaces onto the selector (only the share payload stays, until H2c-3);
// H2c-3 sends the share payload through the selector, so no temporary file is left.
import { describe, expect, it } from 'vitest'
import ts from 'typescript'

// Paths are plain `/`-separated strings (TypeScript's own file names are): the
// app tsconfig has no `@types/node` (see `index.css.test.ts`), so no `node:path`.
const SPA_DIR = decodeURIComponent(new URL('../..', import.meta.url).pathname).replace(/\/$/, '')
const SRC_DIR = `${SPA_DIR}/src`
const HOST_STORE_FILE = `${SRC_DIR}/stores/useHostStore.ts`

/** `fileName` relative to `spa/src`, or null when outside it. */
function srcRelative(fileName: string): string | null {
  return fileName.startsWith(`${SRC_DIR}/`) ? fileName.slice(SRC_DIR.length + 1) : null
}

/** The guarded look fields (H2a: colour / icon; H2b-1: `name`). */
export const GUARDED_FIELDS = ['name', 'colors', 'color', 'icon', 'iconWeight'] as const

export interface GuardHit {
  /** Relative to `spa/src`, `/`-separated. */
  file: string
  line: number
  field: string
  /** The declaration key of the read: a unique lexical path (see the header; `<module>` when none). */
  decl: string
}

// === the detector ===

const ASSIGNMENT_OPERATORS: ReadonlySet<ts.SyntaxKind> = new Set([
  ts.SyntaxKind.EqualsToken,
  ts.SyntaxKind.PlusEqualsToken,
  ts.SyntaxKind.MinusEqualsToken,
  ts.SyntaxKind.AsteriskEqualsToken,
  ts.SyntaxKind.SlashEqualsToken,
  ts.SyntaxKind.BarBarEqualsToken,
  ts.SyntaxKind.AmpersandAmpersandEqualsToken,
  ts.SyntaxKind.QuestionQuestionEqualsToken,
])

/** `node` is written, not read: `node = …`, `node += …`, `delete node`. */
function isWriteTarget(node: ts.Expression): boolean {
  let n: ts.Node = node
  while (ts.isParenthesizedExpression(n.parent)) n = n.parent
  const parent = n.parent
  if (ts.isDeleteExpression(parent)) return true
  return ts.isBinaryExpression(parent) && parent.left === n && ASSIGNMENT_OPERATORS.has(parent.operatorToken.kind)
}

/**
 * The text of a statically known property key: an identifier, a string /
 * numeric literal, or a computed key that is a string / no-substitution
 * template literal (`['icon']`, `` [`icon`] ``). Undefined otherwise (`[k]`).
 */
function nameText(name: ts.Node | undefined): string | undefined {
  if (!name) return undefined
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name) || ts.isStringLiteral(name) || ts.isNumericLiteral(name)) {
    return name.text
  }
  if (ts.isComputedPropertyName(name)) {
    const e = name.expression
    if (ts.isStringLiteral(e) || ts.isNoSubstitutionTemplateLiteral(e) || ts.isNumericLiteral(e)) return e.text
  }
  return undefined
}

/** Expression wrappers that do not change which declaration a value belongs to. */
function isWrapper(n: ts.Node): boolean {
  return (
    ts.isParenthesizedExpression(n) ||
    ts.isAsExpression(n) ||
    ts.isSatisfiesExpression(n) ||
    ts.isNonNullExpression(n) ||
    ts.isTypeAssertionExpression(n)
  )
}

function skipWrappersDown(e: ts.Expression): ts.Expression {
  while (isWrapper(e)) e = (e as ts.ParenthesizedExpression).expression
  return e
}

/** `n` with its wrappers climbed: the node its real parent sees. */
function skipWrappersUp(n: ts.Node): ts.Node {
  while (isWrapper(n.parent)) n = n.parent
  return n
}

function calleeName(e: ts.Expression): string {
  e = skipWrappersDown(e)
  if (ts.isIdentifier(e)) return e.text
  if (ts.isPropertyAccessExpression(e)) return e.name.text
  if (ts.isElementAccessExpression(e) && ts.isStringLiteralLike(e.argumentExpression)) return e.argumentExpression.text
  return '<call>'
}

/** An anonymous function / class, named after where it is bound. */
function anonymousName(n: ts.Node): string {
  const c = skipWrappersUp(n)
  const p = c.parent
  if (ts.isExportAssignment(p)) return 'default'
  if ((ts.isCallExpression(p) || ts.isNewExpression(p)) && p.arguments) {
    const i = p.arguments.indexOf(c as ts.Expression)
    if (i >= 0) return `${calleeName(p.expression)}(arg${i})`
  }
  if (ts.isJsxExpression(p) && ts.isJsxAttribute(p.parent) && ts.isIdentifier(p.parent.name)) return p.parent.name.text
  return '<anonymous>'
}

/** `n` is the initializer of a declaration that is itself a key segment (so `n` adds none). */
function isDeclInitializer(n: ts.Node): boolean {
  const c = skipWrappersUp(n)
  const p = c.parent
  return (
    ((ts.isVariableDeclaration(p) && ts.isIdentifier(p.name)) || ts.isPropertyAssignment(p) || ts.isPropertyDeclaration(p)) &&
    p.initializer === c
  )
}

function isModuleLevel(d: ts.VariableDeclaration): boolean {
  const stmt = d.parent.parent
  return ts.isVariableStatement(stmt) && (ts.isSourceFile(stmt.parent) || ts.isModuleBlock(stmt.parent))
}

/**
 * The base name `n` contributes to a declaration key, or undefined when `n` is
 * not a key segment (see the header for which nodes are).
 */
function segmentBase(n: ts.Node): string | undefined {
  if (ts.isFunctionDeclaration(n) || ts.isClassDeclaration(n)) {
    if (n.name) return n.name.text
    return n.modifiers?.some((m) => m.kind === ts.SyntaxKind.DefaultKeyword) ? 'default' : '<anonymous>'
  }
  if (
    ts.isMethodDeclaration(n) ||
    ts.isGetAccessorDeclaration(n) ||
    ts.isSetAccessorDeclaration(n) ||
    ts.isPropertyAssignment(n) ||
    ts.isPropertyDeclaration(n)
  ) {
    return nameText(n.name) ?? '<computed>'
  }
  if (ts.isConstructorDeclaration(n)) return 'constructor'
  if (ts.isVariableDeclaration(n)) {
    if (!ts.isIdentifier(n.name) || !n.initializer) return undefined
    const init = skipWrappersDown(n.initializer)
    const code = ts.isArrowFunction(init) || ts.isFunctionExpression(init) || ts.isClassExpression(init)
    return code || isModuleLevel(n) ? n.name.text : undefined
  }
  if (ts.isArrowFunction(n) || ts.isFunctionExpression(n) || ts.isClassExpression(n)) {
    if (isDeclInitializer(n)) return undefined
    return !ts.isArrowFunction(n) && n.name ? n.name.text : anonymousName(n)
  }
  return undefined
}

/** container → base name → its key segments of that name, in source order. */
const childSegmentCache = new WeakMap<ts.Node, Map<string, ts.Node[]>>()

/** The key segments directly under `container` (not nested in another segment). */
function childSegments(container: ts.Node): Map<string, ts.Node[]> {
  let byName = childSegmentCache.get(container)
  if (byName) return byName
  const found = new Map<string, ts.Node[]>()
  const visit = (c: ts.Node): void => {
    const base = segmentBase(c)
    if (base === undefined) return void ts.forEachChild(c, visit)
    found.set(base, [...(found.get(base) ?? []), c])
  }
  ts.forEachChild(container, visit)
  byName = found
  childSegmentCache.set(container, byName)
  return byName
}

/** The declaration key of `node`: its enclosing key segments, outermost first (see the header). */
export function enclosingDecl(node: ts.Node): string {
  const chain: ts.Node[] = []
  for (let n: ts.Node | undefined = node.parent; n; n = n.parent) {
    if (segmentBase(n) !== undefined) chain.unshift(n)
  }
  if (chain.length === 0) return '<module>'
  return chain
    .map((seg, i) => {
      const base = segmentBase(seg)!
      const siblings = childSegments(i === 0 ? seg.getSourceFile() : chain[i - 1]).get(base) ?? [seg]
      return siblings.length > 1 ? `${base}#${siblings.indexOf(seg) + 1}` : base
    })
    .join('.')
}

function hostConfigSymbol(program: ts.Program, checker: ts.TypeChecker): ts.Symbol {
  const sf = program.getSourceFile(HOST_STORE_FILE)
  if (!sf) throw new Error(`guard: ${HOST_STORE_FILE} is not in the program`)
  for (const stmt of sf.statements) {
    if (ts.isInterfaceDeclaration(stmt) && stmt.name.text === 'HostConfig') {
      const sym = checker.getSymbolAtLocation(stmt.name)
      if (sym) return sym
    }
  }
  throw new Error('guard: interface HostConfig not found in useHostStore.ts')
}

export interface DetectOptions {
  fields: readonly string[]
  /** Which source files to scan (absolute file name). */
  include: (fileName: string) => boolean
}

/** Every read of a guarded field on a `HostConfig`-typed expression, in `include`d files. */
export function detectHostConfigReads(program: ts.Program, opts: DetectOptions): GuardHit[] {
  const checker = program.getTypeChecker()
  const target = hostConfigSymbol(program, checker)
  const fields = new Set(opts.fields)

  const isHostConfig = (type: ts.Type): boolean => {
    if (type.isUnionOrIntersection()) return type.types.some(isHostConfig)
    return type.getSymbol() === target || type.aliasSymbol === target
  }

  const hits: GuardHit[] = []
  for (const sf of program.getSourceFiles()) {
    if (sf.isDeclarationFile || !opts.include(sf.fileName)) continue
    const file = srcRelative(sf.fileName) ?? sf.fileName
    const hit = (node: ts.Node, field: string) =>
      hits.push({
        file,
        line: sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1,
        field,
        decl: enclosingDecl(node),
      })

    const visit = (node: ts.Node): void => {
      if (ts.isPropertyAccessExpression(node)) {
        const field = node.name.text
        if (fields.has(field) && !isWriteTarget(node) && isHostConfig(checker.getTypeAtLocation(node.expression))) {
          hit(node.name, field)
        }
      } else if (ts.isElementAccessExpression(node)) {
        const arg = node.argumentExpression
        if (ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg)) {
          const field = arg.text
          if (fields.has(field) && !isWriteTarget(node) && isHostConfig(checker.getTypeAtLocation(node.expression))) {
            hit(arg, field)
          }
        }
      } else if (ts.isObjectBindingPattern(node)) {
        const patternType = checker.getTypeAtLocation(node)
        if (isHostConfig(patternType)) {
          for (const el of node.elements) {
            if (el.dotDotDotToken) continue
            // `{ icon }`, `{ icon: v }`, `{ 'icon': v }`, `{ ['icon']: v }`; `{ [k]: v }` stays unresolved.
            const field = nameText(el.propertyName ?? el.name)
            if (field !== undefined && fields.has(field)) hit(el, field)
          }
        }
      }
      ts.forEachChild(node, visit)
    }
    visit(sf)
  }
  return hits.sort((a, b) => (a.file < b.file ? -1 : a.file > b.file ? 1 : a.line - b.line))
}

// === programs ===

function appCompilerOptions(): ts.CompilerOptions {
  const configPath = `${SPA_DIR}/tsconfig.app.json`
  const read = ts.readConfigFile(configPath, (p) => ts.sys.readFile(p))
  if (read.error) throw new Error(ts.flattenDiagnosticMessageText(read.error.messageText, '\n'))
  return ts.parseJsonConfigFileContent(read.config, ts.sys, SPA_DIR, undefined, configPath).options
}

/** `spa/src` production sources only. */
export function isGuardedSource(fileName: string): boolean {
  const rel = srcRelative(fileName)
  if (rel === null) return false
  if (!/\.(ts|tsx)$/.test(rel) || rel.endsWith('.d.ts')) return false
  if (/\.test\.[^/]+$/.test(rel)) return false
  if (rel.split('/').includes('__tests__')) return false
  const base = rel.split('/').pop()!
  return base !== 'test-setup.ts' && !base.startsWith('test-utils')
}

function repoProgram(): ts.Program {
  const configPath = `${SPA_DIR}/tsconfig.app.json`
  const read = ts.readConfigFile(configPath, (p) => ts.sys.readFile(p))
  const parsed = ts.parseJsonConfigFileContent(read.config, ts.sys, SPA_DIR, undefined, configPath)
  return ts.createProgram({ rootNames: parsed.fileNames.filter(isGuardedSource), options: parsed.options })
}

/**
 * An in-memory program: `files` (name → source) live as virtual files inside
 * `spa/src/lib/__host_look_guard_fixture__/`, so `../../stores/useHostStore`
 * resolves to the REAL `HostConfig`. Everything else is read from disk.
 */
function fixtureProgram(files: Record<string, string>): { program: ts.Program; dir: string } {
  const dir = `${SRC_DIR}/lib/__host_look_guard_fixture__`
  const options = appCompilerOptions()
  const virtual = new Map(Object.entries(files).map(([name, text]) => [`${dir}/${name}`, text]))
  const host = ts.createCompilerHost(options, true)
  const baseGetSourceFile = host.getSourceFile.bind(host)
  const baseFileExists = host.fileExists.bind(host)
  const baseReadFile = host.readFile.bind(host)
  host.fileExists = (f) => virtual.has(f) || baseFileExists(f)
  host.readFile = (f) => virtual.get(f) ?? baseReadFile(f)
  host.getSourceFile = (f, lang, onError, shouldCreate) => {
    const text = virtual.get(f)
    return text !== undefined ? ts.createSourceFile(f, text, lang, true) : baseGetSourceFile(f, lang, onError, shouldCreate)
  }
  return { program: ts.createProgram({ rootNames: [...virtual.keys()], options, host }), dir }
}

// === fixture half ===

const HEADER = `import type { HostConfig } from '../../stores/useHostStore'\ndeclare const h: HostConfig\n`

/** name → [snippet, the fields it must flag (in order)]. */
const FIXTURES: Record<string, [string, string[]]> = {
  // flagged
  'property-read.ts': [`export const v = h.icon`, ['icon']],
  'optional-chain.ts': [`declare const m: HostConfig | undefined\nexport const v = m?.iconWeight`, ['iconWeight']],
  'string-literal-element.ts': [`export const v = h['icon']`, ['icon']],
  'record-optional-chain.ts': [
    `declare const hosts: Record<string, HostConfig | undefined>\ndeclare const id: string\nexport const v = hosts[id]?.colors`,
    ['colors'],
  ],
  'union-with-other.ts': [`declare const u: HostConfig | { colors?: string; name: string }\nexport const v = u.colors`, ['colors']],
  'intersection.ts': [`declare const w: HostConfig & { aliases?: string[] }\nexport const v = w.color`, ['color']],
  'object-destructure.ts': [`const { icon, colors: c } = h\nexport const v = [icon, c]`, ['icon', 'colors']],
  'nested-destructure.ts': [`declare const o: { host: HostConfig }\nconst { host: { iconWeight } } = o\nexport const v = iconWeight`, ['iconWeight']],
  'parameter-destructure.ts': [`export const f = ({ icon }: HostConfig) => icon`, ['icon']],
  'alias.ts': [`type H = HostConfig\ndeclare const a: H\nexport const v = a.icon`, ['icon']],
  'read-in-rhs.ts': [`declare const next: HostConfig\nnext.icon = h.icon`, ['icon']],
  'computed-binding-literal.ts': [
    `const { ['icon']: v1 } = h\nconst { [\`color\`]: v2 } = h\nexport const v = [v1, v2]`,
    ['icon', 'color'],
  ],
  'computed-parameter-literal.ts': [`export const f = ({ ['colors']: c }: HostConfig) => c`, ['colors']],
  'name-read.ts': [
    `declare const m: HostConfig | undefined\nconst { name } = h\nexport const v = [name, m?.name, h['name']]`,
    ['name', 'name', 'name'],
  ],
  // not flagged
  'assignment-left.ts': [`declare const next: HostConfig\nnext.icon = 'Laptop'\nnext.color ??= '#ffffff'`, []],
  'delete.ts': [`declare const next: HostConfig\ndelete next.icon\ndelete next['colors']`, []],
  'object-literal.ts': [`export const v: Partial<HostConfig> = { icon: 'x', color: '#000000' }`, []],
  'other-type.ts': [`declare const f: { icon: string; colors: string }\nexport const v = [f.icon, f.colors]`, []],
  'unguarded-field.ts': [`export const v = [h.id, h.ip, h.daemonId]`, []],
  // KNOWN FALSE NEGATIVES — pinned blind spots, NOT flagged on purpose
  'blind-variable-key.ts': [`declare const k: 'icon' | 'colors'\nexport const v = h[k]`, []],
  'blind-cast.ts': [`export const v = (h as { icon?: string }).icon`, []],
  'blind-pick.ts': [`const p: Pick<HostConfig, 'icon'> = h\nexport const v = p.icon`, []],
  'blind-partial.ts': [`const p: Partial<HostConfig> = h\nexport const v = p.icon`, []],
  'blind-spread.ts': [`export const v = ({ ...h }).icon`, []],
  'blind-destructure-assign.ts': [`let icon: string | undefined\n;({ icon } = h)\nexport const v = icon`, []],
  'blind-computed-binding-variable.ts': [`const k = 'icon'\nconst { [k]: v1 } = h\nexport const v = v1`, []],
}

describe('host-look guard — detector fixtures', { timeout: 60_000 }, () => {
  const { program, dir } = fixtureProgram(
    Object.fromEntries(Object.entries(FIXTURES).map(([name, [snippet]]) => [name, HEADER + snippet + '\n'])),
  )
  const hits = detectHostConfigReads(program, {
    fields: GUARDED_FIELDS,
    include: (f) => f.startsWith(`${dir}/`),
  })

  it('the fixture program type-checks (the snippets mean what they say)', () => {
    const errors = ts
      .getPreEmitDiagnostics(program)
      .filter((d) => d.file?.fileName.startsWith(`${dir}/`))
      .map((d) => `${d.file!.fileName.slice(dir.length + 1)}: ${ts.flattenDiagnosticMessageText(d.messageText, ' ')}`)
    expect(errors).toEqual([])
  })

  it.each(Object.entries(FIXTURES))('%s', (name, [, expected]) => {
    const got = hits.filter((h) => h.file.endsWith(`/${name}`)).map((h) => h.field)
    expect(got).toEqual(expected)
  })
})

/** name → [snippet, the `decl` of each hit (in order)]. */
const DECL_FIXTURES: Record<string, [string, string[]]> = {
  'decl-module.ts': [`console.log(h.color)\nexport const v = h.icon`, ['<module>', 'v']],
  'decl-function.ts': [`export function f() { const local = h.color; return [local, h.icon] }`, ['f', 'f']],
  'decl-arrow-const.ts': [`export const f = () => [h.icon, h.color]`, ['f', 'f']],
  'decl-nested.ts': [`export function outer() { function inner() { return h.icon } return [inner(), h.color] }`, ['outer.inner', 'outer']],
  'decl-property-key.ts': [
    `export const store = { setIcon: (x: HostConfig) => [1].map(() => x.icon), other: () => h.colors }`,
    ['store.setIcon.map(arg0)', 'store.other'],
  ],
  'decl-method.ts': [`export class C { m() { return h.iconWeight } get g() { return h.icon } }`, ['C.m', 'C.g']],
  'decl-destructure.ts': [`export function f() { const { icon, color } = h; return [icon, color] }`, ['f', 'f']],
  'decl-same-name-ordinal.ts': [
    `export function o() { if (h.id) { function inner() { return h.icon } return inner() } function inner() { return h.color } return inner() }`,
    ['o.inner#1', 'o.inner#2'],
  ],
  'decl-anonymous.ts': [
    `export default function () { return h.icon }\n;[h].forEach(() => h.color)\n;[h].forEach(() => h.colors)\nexport const w = [() => h.iconWeight]`,
    ['default', 'forEach(arg0)#1', 'forEach(arg0)#2', 'w.<anonymous>'],
  ],
  'decl-computed-names.ts': [
    `declare const k: string\nexport const o = { ['lit']: () => h.icon, [k]: () => h.color }`,
    ['o.lit', 'o.<computed>'],
  ],
}

describe('host-look guard — enclosing declaration', { timeout: 60_000 }, () => {
  const { program, dir } = fixtureProgram(
    Object.fromEntries(Object.entries(DECL_FIXTURES).map(([name, [snippet]]) => [name, HEADER + snippet + '\n'])),
  )
  const hits = detectHostConfigReads(program, { fields: GUARDED_FIELDS, include: (f) => f.startsWith(`${dir}/`) })

  it.each(Object.entries(DECL_FIXTURES))('%s', (name, [, expected]) => {
    expect(hits.filter((h) => h.file.endsWith(`/${name}`)).map((h) => h.decl)).toEqual(expected)
  })
})

// === the allowlist check ===

type GuardedField = (typeof GUARDED_FIELDS)[number]
/** file (relative to `spa/src`) → enclosing declaration → field → exact read count. */
export type Allowlist = Record<string, Record<string, Partial<Record<GuardedField, number>>>>

/**
 * Every difference between the hit multiset and `allowlist`, keyed by
 * (file, decl, field): a key with more reads than allowlisted, fewer, or not
 * allowlisted at all. Empty = exact match.
 */
export function allowlistProblems(hits: readonly GuardHit[], allowlist: Allowlist): string[] {
  const key = (file: string, decl: string, field: string) => JSON.stringify([file, decl, field])
  const got = new Map<string, GuardHit[]>()
  for (const h of hits) {
    const k = key(h.file, h.decl, h.field)
    got.set(k, [...(got.get(k) ?? []), h])
  }
  const want = new Map<string, number>()
  for (const [file, decls] of Object.entries(allowlist)) {
    for (const [decl, counts] of Object.entries(decls)) {
      for (const [field, n] of Object.entries(counts)) want.set(key(file, decl, field), n ?? 0)
    }
  }
  const problems: string[] = []
  for (const k of new Set([...got.keys(), ...want.keys()])) {
    const list = got.get(k) ?? []
    const n = want.get(k) ?? 0
    if (list.length === n) continue
    const [file, decl, field] = JSON.parse(k) as [string, string, string]
    const where = list.map((h) => `${h.file}:${h.line}`).join(', ') || 'none'
    problems.push(
      want.has(k)
        ? `${file} ${decl} ${field}: ${list.length} read(s), allowlisted ${n} — ${where}`
        : `${file} ${decl} ${field}: ${list.length} read(s), not allowlisted — ${where}`,
    )
  }
  return problems.sort()
}

describe('host-look guard — allowlistProblems', { timeout: 60_000 }, () => {
  const scan = (src: string) => {
    const { program, dir } = fixtureProgram({ 'swap.ts': HEADER + src + '\n' })
    return detectHostConfigReads(program, { fields: GUARDED_FIELDS, include: (f) => f.startsWith(`${dir}/`) })
  }
  const FILE = 'lib/__host_look_guard_fixture__/swap.ts'
  const allow: Allowlist = { [FILE]: { keep: { icon: 1 }, twice: { color: 2 } } }
  const baseline = [
    `export function keep(x: HostConfig) { return x.icon }`,
    `export function other(x: HostConfig) { return x.ip }`,
    `export function twice(x: HostConfig) { return [x.color, x.color] }`,
  ].join('\n')

  it('the baseline matches its allowlist', () => {
    expect(allowlistProblems(scan(baseline), allow)).toEqual([])
  })

  it('a same-file, same-count swap into another declaration fails', () => {
    const swapped = baseline
      .replace('keep(x: HostConfig) { return x.icon }', 'keep(x: HostConfig) { return x.ip }')
      .replace('other(x: HostConfig) { return x.ip }', 'other(x: HostConfig) { return x.icon }')
    expect(swapped).not.toBe(baseline)
    expect(allowlistProblems(scan(swapped), allow)).toEqual([
      `${FILE} keep icon: 0 read(s), allowlisted 1 — none`,
      `${FILE} other icon: 1 read(s), not allowlisted — ${FILE}:4`,
    ])
  })

  it('the count inside one declaration must match too', () => {
    const once = baseline.replace('[x.color, x.color]', '[x.color, x.ip]')
    expect(allowlistProblems(scan(once), allow)).toEqual([`${FILE} twice color: 1 read(s), allowlisted 2 — ${FILE}:5`])
  })

  it('a read in a file that is not allowlisted fails', () => {
    expect(allowlistProblems(scan(baseline), {})).toHaveLength(2)
  })
})

/** The allowlist a scan would need to pass exactly (whatever the key format). */
function allowlistOf(hits: readonly GuardHit[]): Allowlist {
  const out: Allowlist = {}
  for (const h of hits) {
    const counts = ((out[h.file] ??= {})[h.decl] ??= {})
    counts[h.field as GuardedField] = (counts[h.field as GuardedField] ?? 0) + 1
  }
  return out
}

/**
 * name → [baseline, swapped]: `swapped` moves the `icon` read into a declaration
 * that shares a NAME (or anonymity) with the one it came from, keeping every
 * file-wide count. The allowlist measured on `baseline` must reject `swapped`.
 */
const SWAPS: Record<string, [string, string]> = {
  'same-named nested functions, different parents': [
    `export function a(x: HostConfig) { function inner() { return x.icon } return inner() }\n` +
      `export function b(x: HostConfig) { function inner() { return x.ip } return inner() }`,
    `export function a(x: HostConfig) { function inner() { return x.ip } return inner() }\n` +
      `export function b(x: HostConfig) { function inner() { return x.icon } return inner() }`,
  ],
  'same-named nested functions, same parent': [
    `export function o(x: HostConfig) { if (x.id) { function inner() { return x.icon } return inner() } else { function inner() { return x.ip } return inner() } }`,
    `export function o(x: HostConfig) { if (x.id) { function inner() { return x.ip } return inner() } else { function inner() { return x.icon } return inner() } }`,
  ],
  'same-named methods, two classes': [
    `export class A { m(x: HostConfig) { return x.icon } }\nexport class B { m(x: HostConfig) { return x.ip } }`,
    `export class A { m(x: HostConfig) { return x.ip } }\nexport class B { m(x: HostConfig) { return x.icon } }`,
  ],
  'same-named methods, two named object literals': [
    `export const A = { m(x: HostConfig) { return x.icon } }\nexport const B = { m(x: HostConfig) { return x.ip } }`,
    `export const A = { m(x: HostConfig) { return x.ip } }\nexport const B = { m(x: HostConfig) { return x.icon } }`,
  ],
  'same-named methods, two anonymous object literals': [
    `declare function reg(o: object): void\nreg({ m(x: HostConfig) { return x.icon } })\nreg({ m(x: HostConfig) { return x.ip } })`,
    `declare function reg(o: object): void\nreg({ m(x: HostConfig) { return x.ip } })\nreg({ m(x: HostConfig) { return x.icon } })`,
  ],
  'anonymous default export vs anonymous callback': [
    `export default function (x: HostConfig) { return x.icon }\nexport const r = [h].map((x) => x.ip)`,
    `export default function (x: HostConfig) { return x.ip }\nexport const r = [h].map((x) => x.icon)`,
  ],
  'two anonymous callbacks': [
    `;[h].forEach((x) => x.icon)\n;[h].forEach((x) => x.ip)`,
    `;[h].forEach((x) => x.ip)\n;[h].forEach((x) => x.icon)`,
  ],
}

describe('host-look guard — declaration keys are unique', { timeout: 60_000 }, () => {
  const { program, dir } = fixtureProgram(
    Object.fromEntries(
      Object.values(SWAPS).flatMap(([base, swapped], i) => [
        [`swap-${i}-base.ts`, HEADER + base + '\n'],
        [`swap-${i}-swapped.ts`, HEADER + swapped + '\n'],
        // the baseline behind an unrelated edit: a new declaration above everything
        [`swap-${i}-shifted.ts`, HEADER + `export function unrelated(x: HostConfig) { return x.ip }\n` + base + '\n'],
      ]),
    ),
  )
  const hits = detectHostConfigReads(program, { fields: GUARDED_FIELDS, include: (f) => f.startsWith(`${dir}/`) })
  const of = (name: string) =>
    hits.filter((h) => h.file.endsWith(`/${name}`)).map((h) => ({ ...h, file: 'swap.ts' }))

  it('the swap fixtures type-check', () => {
    const errors = ts
      .getPreEmitDiagnostics(program)
      .filter((d) => d.file?.fileName.startsWith(`${dir}/`))
      .map((d) => `${d.file!.fileName.slice(dir.length + 1)}: ${ts.flattenDiagnosticMessageText(d.messageText, ' ')}`)
    expect(errors).toEqual([])
  })

  it.each(Object.keys(SWAPS).map((name, i) => [name, i] as const))('%s', (_name, i) => {
    const allow = allowlistOf(of(`swap-${i}-base.ts`))
    expect(of(`swap-${i}-base.ts`).map((h) => h.field)).toEqual(['icon'])
    expect(allowlistProblems(of(`swap-${i}-shifted.ts`), allow)).toEqual([])
    expect(allowlistProblems(of(`swap-${i}-swapped.ts`), allow)).not.toEqual([])
  })
})

// === repo half ===

/**
 * The only production reads of a look field off `HostConfig`: file →
 * declaration key → field → exact count. Colour / icon measured at H2a T4,
 * `name` at H2b-1 T1; `stores/useHostStore.ts` re-measured at H2c-2 T2.
 * A widening also edits the pinned copy below — same file, so a tripwire made
 * visible in review, not an enforcement boundary (see the header).
 */
export const ALLOWLIST: Allowlist = {
  // the store: since H2c-2 its writers edit the look store's entry and read `HostConfig` only to seed an absent
  // entry — the one destructure in `lookSeedOf` (the writers' own reads are on the look entry, not `HostConfig`)
  'stores/useHostStore.ts': {
    lookSeedOf: { name: 1, colors: 1, color: 1, icon: 1, iconWeight: 1 },
  },
  // the persisted-state sanitiser
  'lib/host-color.ts': {
    sanitizeHostConfig: { colors: 1, color: 1, icon: 1, iconWeight: 1 },
  },
  // THE selector
  'lib/host-look.ts': {
    lookOfHost: { name: 1, colors: 1, color: 1, icon: 1, iconWeight: 1 },
  },
  // the first-run migration: `HostConfig` look as a SOURCE (spec §4.4) — its one destructure (H2c-2 T3)
  'lib/host-look-migration.ts': {
    lookOfConfig: { name: 1, colors: 1, color: 1, icon: 1, iconWeight: 1 },
  },

  // --- TEMPORARY (see `TEMPORARY_FILES`) --- none since H2c-3 (the share payload reads the selector).
}

/**
 * The spec §4.2 exception — plus the first-run migration, which spec §4.4 names as the one other SOURCE reader of
 * `HostConfig` looks (H2c-2): the only files the allowlist may name for good.
 */
const PERMANENT_FILES = ['stores/useHostStore.ts', 'lib/host-color.ts', 'lib/host-look.ts', 'lib/host-look-migration.ts']

/**
 * Every other file the allowlist may name, and the PR that takes it out. Each
 * reads `name` only (colour / icon have no temporary reader). Empty since H2c-3
 * took out the last one (`lib/host-transfer-plan.ts`, the share payload).
 */
const TEMPORARY_FILES: Record<string, string> = {}

describe('host-look guard — repo', { timeout: 60_000 }, () => {
  it('the look reads of HostConfig are exactly the allowlist (file, declaration, field, count)', () => {
    const hits = detectHostConfigReads(repoProgram(), { fields: GUARDED_FIELDS, include: isGuardedSource })
    expect(allowlistProblems(hits, ALLOWLIST)).toEqual([])
  })

  it('the allowlist is pinned verbatim (a widening shows up as a second, explicit edit here)', () => {
    expect(ALLOWLIST).toEqual({
      'stores/useHostStore.ts': {
        lookSeedOf: { name: 1, colors: 1, color: 1, icon: 1, iconWeight: 1 },
      },
      'lib/host-color.ts': {
        sanitizeHostConfig: { colors: 1, color: 1, icon: 1, iconWeight: 1 },
      },
      'lib/host-look.ts': {
        lookOfHost: { name: 1, colors: 1, color: 1, icon: 1, iconWeight: 1 },
      },
      'lib/host-look-migration.ts': {
        lookOfConfig: { name: 1, colors: 1, color: 1, icon: 1, iconWeight: 1 },
      },
    })
  })

  it('the temporary files are pinned verbatim, each with the PR that removes it (none left since H2c-3)', () => {
    expect(TEMPORARY_FILES).toEqual({})
  })

  it('outside the permanent four (spec §4.2 three + the §4.4 migration), the allowlisted files are exactly the temporary list', () => {
    const others = Object.keys(ALLOWLIST).filter((f) => !PERMANENT_FILES.includes(f))
    expect(others.sort()).toEqual(Object.keys(TEMPORARY_FILES).sort())
  })

  it('a temporary file is allowlisted for `name` only', () => {
    const fields = Object.keys(TEMPORARY_FILES).flatMap((f) =>
      Object.values(ALLOWLIST[f] ?? {}).flatMap((counts) => Object.keys(counts)),
    )
    expect(fields.filter((f) => f !== 'name')).toEqual([])
  })
})
