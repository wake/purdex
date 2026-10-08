// hooks/lease.test.ts — the heavy-command classifier (host-resource-lease plan Task 2.1), run by
// `claude plugin test cmd/pdx/plugin/purdex`. Pure functions, so no engine fakes: a table of commands
// and what they are.
import { test, expect } from 'claude-code/testing'
import { classify, rewriteMaxWorkers } from './lease.js'

const kind = (c: string) => classify(c)?.kind ?? null

const KINDS: [string, string | null][] = [
  // test-full
  ['cd spa && npx vitest run', 'test-full'],
  ['npx vitest run', 'test-full'],
  ['pnpm exec vitest run', 'test-full'],
  ['pnpm -C spa exec vitest run --reporter=dot', 'test-full'],
  ['yarn vitest run', 'test-full'],
  ['vitest run', 'test-full'],
  ['FOO=1 npx vitest run', 'test-full'],
  ['timeout 600 npx vitest run', 'test-full'],
  ['cd spa && npx vitest run 2>&1 | tail -30', 'test-full'],
  ['npx vitest run --maxWorkers=3', 'test-full'],
  ['go test -race ./...', 'test-full'],
  ['go test ./...', 'test-full'],
  ['cd cmd && go test ./internal/...', 'test-full'],
  ['make test', 'test-full'],
  ['bash -c "cd spa && npx vitest run"', 'test-full'],
  // narrowed vitest is not heavy
  ['npx vitest run src/lib/foo.test.ts', null],
  ['cd spa && npx vitest run src/lib/foo.test.ts src/lib/bar.test.ts', null],
  ['npx vitest run -t "x"', null],
  ['npx vitest run --testNamePattern x', null],
  ['npx vitest run --project spa', null],
  ['npx vitest run --changed', null],
  ['npx vitest run --dom src/foo.test.ts', null],
  ['npx vitest run --logHeapUsage src/foo.test.ts', null],
  ['npx vitest', null],
  ['npx vitest watch', null],
  ['npx vitest related src/a.ts', null],
  // go
  ['go test -race ./internal/module/team/', 'test-pkg'],
  ['go test -race ./internal/module/team', 'test-pkg'],
  ['go test ./cmd/pdx -run TestX', null],
  ['go test -race ./cmd/pdx -run TestX', null],
  ['go test ./internal/module/team/', null],
  ['go test -race ./a ./b', null],
  ['go test -count=1 ./cmd/pdx', null],
  ['go build ./...', null],
  // build
  ['cd spa && pnpm run build', 'build'],
  ['pnpm build', 'build'],
  ['npm run build', 'build'],
  ['pnpm run electron:build', 'build'],
  ['tsc -b', 'build'],
  ['npx tsc --build', 'build'],
  ['npx vite build', 'build'],
  ['npx electron-vite build', 'build'],
  ['tsc --noEmit -p tsconfig.app.json', null],
  ['pnpm install', null],
  // lint-full
  ['go vet ./...', 'lint-full'],
  ['npx eslint .', 'lint-full'],
  ['cd spa && pnpm run lint', 'lint-full'],
  ['npx eslint src/lib/foo.ts', null],
  ['go vet ./cmd/pdx', null],
  // heaviest segment decides
  ['pnpm run lint && npx vitest run', 'test-full'],
  ['go vet ./... && pnpm run build', 'build'],
  // not commands, wrapped, background, unparseable
  ['pdx lease run --kind build -- pnpm run build', null],
  ['cd spa && pdx lease run --kind test-full -- npx vitest run', null],
  ['echo "npx vitest run"', null],
  ['pdx lease run --kind build -- pnpm run build && npx vitest run', 'test-full'],
  ['pdx lease run --kind build -- pnpm run build && ls', null],
  ["echo 'pnpm run build'", null],
  ['grep -r "vitest run" docs', null],
  ['ls', null],
  ['', null],
  ['npx vitest run "unterminated', null],
  ['npx vitest run &', null],
  ['pnpm run build & sleep 1', null],
  ['npx vitest run > out.txt 2>&1', 'test-full'],
  // attached redirections end the word
  ['npx vitest run>out.txt', 'test-full'],
  ['npx vitest run>>out.txt', 'test-full'],
  ['npx vitest run 2>/dev/null', 'test-full'],
  ['npx vitest run &>out.txt', 'test-full'],
  ['npx vitest run <in.txt', 'test-full'],
  ['npx vitest run src/a.test.ts>out.txt', null],
  // substitutions and other shells run the command too
  ['echo $(npx vitest run)', 'test-full'],
  ['echo "$(npx vitest run)"', 'test-full'],
  ['echo `npx vitest run`', 'test-full'],
  ['x=$(cd spa && pnpm run build)', 'build'],
  ["echo '$(npx vitest run)'", null],
  ['fish -c "npx vitest run"', 'test-full'],
  ['echo $(echo $(npx vitest run))', 'test-full'],
  // heredoc bodies and comments are data
  ['cat <<EOF\nnpx vitest run\nEOF', null],
  ["cat <<'EOF'\nnpx vitest run\nEOF\nnpx vitest run", 'test-full'],
  ['cat <<-EOF\n\tpnpm run build\n\tEOF', null],
  ['cat <<A <<B\nnpx vitest run\nA\npnpm run build\nB', null],
  ['cat > f <<EOF\nx\nEOF\ngo vet ./...', 'lint-full'],
  ['echo ok # npx vitest run', null],
  ['# npx vitest run\nls', null],
  ['ls # ; npx vitest run', null],
  ['echo a#b && npx vitest run', 'test-full'],
]

for (const [cmd, want] of KINDS) {
  test(`classify ${JSON.stringify(cmd)} → ${want}`, () => {
    expect(kind(cmd)).toBe(want)
  })
}

test('classify: a non-string command is not heavy', () => {
  expect(classify(undefined as unknown as string)).toBe(null)
  expect(classify(42 as unknown as string)).toBe(null)
})

test('classify: needsMaxWorkers says a full vitest run has no worker limit', () => {
  expect(classify('npx vitest run')?.needsMaxWorkers).toBe(true)
  expect(classify('npx vitest run --maxWorkers=3')?.needsMaxWorkers).toBe(false)
  expect(classify('npx vitest run --max-workers 2')?.needsMaxWorkers).toBe(false)
  expect(classify('npx vitest run --poolOptions.threads.maxThreads=2')?.needsMaxWorkers).toBe(false)
  expect(classify('bash -c "npx vitest run"')?.needsMaxWorkers).toBe(true)
  expect(classify('pnpm run build')?.needsMaxWorkers).toBe(false)
  expect(classify('go test -race ./...')?.needsMaxWorkers).toBe(false)
})

const REWRITES: [string, string][] = [
  ['cd spa && npx vitest run', 'cd spa && npx vitest run --maxWorkers=3'],
  ['FOO=1 npx vitest run', 'FOO=1 npx vitest run --maxWorkers=3'],
  ['npx vitest run --reporter=dot', 'npx vitest run --reporter=dot --maxWorkers=3'],
  ['npx vitest run 2>&1 | tail -30', 'npx vitest run --maxWorkers=3 2>&1 | tail -30'],
  ['npx vitest run > out.txt 2>&1', 'npx vitest run --maxWorkers=3 > out.txt 2>&1'],
  ['cd spa && npx vitest run && pnpm run build', 'cd spa && npx vitest run --maxWorkers=3 && pnpm run build'],
  ['bash -c "cd spa && npx vitest run"', 'bash -c "cd spa && npx vitest run --maxWorkers=3"'],
  ["sh -c 'npx vitest run 2>&1 | tail'", "sh -c 'npx vitest run --maxWorkers=3 2>&1 | tail'"],
  ['npx vitest run>out.txt', 'npx vitest run --maxWorkers=3>out.txt'],
  ['npx vitest run 2>/dev/null', 'npx vitest run --maxWorkers=3 2>/dev/null'],
  ['pdx lease run --kind build -- ls && npx vitest run', 'pdx lease run --kind build -- ls && npx vitest run --maxWorkers=3'],
  ['npx vitest run; npx vitest run', 'npx vitest run --maxWorkers=3; npx vitest run --maxWorkers=3'],
]

for (const [cmd, want] of REWRITES) {
  test(`rewriteMaxWorkers ${JSON.stringify(cmd)}`, () => {
    expect(rewriteMaxWorkers(cmd)).toEqual({ command: want, changed: true })
  })
}

const UNCHANGED = [
  'cat <<EOF\nnpx vitest run\nEOF',
  'echo ok # npx vitest run',
  'ls # ; npx vitest run',
  'npx vitest run --maxWorkers=3',
  'npx vitest run --max-workers=2',
  'npx vitest run src/lib/foo.test.ts',
  'npx vitest run -t "x"',
  'go test -race ./...',
  'pnpm run build',
  'echo "npx vitest run"',
  'npx vitest run "unterminated',
  'npx vitest run &',
  'pdx lease run --kind test-full -- npx vitest run',
]

for (const cmd of UNCHANGED) {
  test(`rewriteMaxWorkers leaves ${JSON.stringify(cmd)} alone`, () => {
    expect(rewriteMaxWorkers(cmd)).toEqual({ command: cmd, changed: false })
  })
}

test('rewriteMaxWorkers: the rewritten command still classifies as test-full with the limit given', () => {
  const r = rewriteMaxWorkers('cd spa && npx vitest run 2>&1 | tail -30')
  expect(classify(r.command)).toEqual({ kind: 'test-full', needsMaxWorkers: false })
})
