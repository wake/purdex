import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

// F4: restrict write-side contribution registry APIs to the canonical
// owners. The @internal JSDoc in `settings-contribution-registry.ts` has
// no enforcement; this lints against direct imports from any other file.
// See #539 and the PR-2 design doc §5.3/§7.2.
const RESTRICTED_CONTRIBUTION_WRITE_IMPORTS = {
  patterns: [
    {
      group: [
        '**/settings-contribution-registry',
        '**/settings-contribution-registry.ts',
      ],
      importNames: [
        'registerSettingsContribution',
        'clearContributions',
        'assertValidSettingsContribution',
      ],
      message:
        'Write-side contribution registry APIs may only be imported from ' +
        'dispatch-settings-contributions.ts, settings-section-registry.ts, ' +
        'or test files. Use listContributions() / getContribution() for ' +
        'read access; module authors should declare `settings: [...]` on ' +
        'their ModuleDefinition instead.',
    },
  ],
}

export default defineConfig([
  globalIgnores(['dist', 'src/features/workspace/generated']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    rules: {
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrorsIgnorePattern: '^_' },
      ],
      'no-restricted-imports': ['error', RESTRICTED_CONTRIBUTION_WRITE_IMPORTS],
    },
  },
  // Whitelisted callers of the write-side registry APIs. These three files
  // form the canonical write path:
  //   - dispatch-settings-contributions.ts: atomic validation + commit
  //   - settings-section-registry.ts: legacy adapter
  // Tests disable the rule so they can stage isolated fixtures without
  // going through the dispatch plumbing.
  {
    files: [
      '**/dispatch-settings-contributions.ts',
      '**/settings-section-registry.ts',
      '**/*.test.ts',
      '**/*.test.tsx',
      // Test-only helpers under __tests__/ (e.g. shared bootstrap harness)
      // need the same access as test files themselves.
      '**/__tests__/**/*.ts',
      '**/__tests__/**/*.tsx',
    ],
    rules: {
      'no-restricted-imports': 'off',
    },
  },
  // Test-only modules under __tests__/ act as shared fixtures: they export the
  // stub components a `vi.mock` factory mounts ALONGSIDE the spies and helpers
  // those stubs write to. That is exactly the mix react-refresh forbids in app
  // code, and it is meaningless here — nothing under __tests__/ is ever hot
  // reloaded.
  {
    files: ['**/__tests__/**/*.tsx'],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },
])
