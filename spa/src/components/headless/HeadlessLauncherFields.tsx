// spa/src/components/headless/HeadlessLauncherFields.tsx — the Headless
// launcher's form fields (P-C spec §4.2). Purely presentational: every value
// and verdict comes from the parent, which owns the submit path.
import { Lightning } from '@phosphor-icons/react'
import type { NexCapabilities } from '../../lib/nex/types'
import type { SubPathVerdict } from '../../lib/nex/cwd-input'
import { useI18nStore } from '../../stores/useI18nStore'

export interface HeadlessLauncherFieldsProps {
  caps: NexCapabilities
  brief: string
  usedBytes: number
  maxBytes: number
  root: string
  sub: string
  subVerdict: SubPathVerdict
  profile: string
  busy: boolean
  canSubmit: boolean
  error: string
  onBrief: (v: string) => void
  onRoot: (v: string) => void
  onSub: (v: string) => void
  onProfile: (v: string) => void
  onSubmit: () => void
}

const FIELD = 'w-full bg-surface-input border border-border-default rounded px-2 py-1 text-sm text-text-primary focus:border-border-active focus:outline-none disabled:opacity-50'
const LABEL = 'text-xs text-text-secondary'

export function HeadlessLauncherFields(p: HeadlessLauncherFieldsProps) {
  const t = useI18nStore((s) => s.t)
  const noRoots = p.caps.roots.length === 0
  const locked = p.busy || noRoots
  const overLimit = p.usedBytes > p.maxBytes
  const rootKind = p.caps.roots.find((r) => r.path === p.root)?.kind

  const handleBriefKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.nativeEvent.isComposing) {
      e.preventDefault()
      p.onSubmit()
    }
  }

  return (
    <form
      data-testid="headless-launcher"
      className="flex flex-col gap-2 p-2 bg-surface-secondary border border-border-default rounded-md"
      onSubmit={(e) => { e.preventDefault(); p.onSubmit() }}
    >
      {noRoots && (
        <p data-testid="headless-no-roots" className="text-xs text-text-muted">{t('newtab.headless.no_roots')}</p>
      )}

      <label className="flex flex-col gap-1">
        <span className="flex items-baseline justify-between">
          <span className={LABEL}>{t('newtab.headless.brief')}</span>
          <span data-testid="headless-bytes" className={`text-xs ${overLimit ? 'text-red-400' : 'text-text-muted'}`}>
            {t('newtab.headless.brief_bytes', { used: p.usedBytes, max: p.maxBytes })}
          </span>
        </span>
        <textarea
          data-testid="headless-brief"
          rows={3}
          value={p.brief}
          disabled={locked}
          spellCheck={false}
          placeholder={t('newtab.headless.brief_placeholder')}
          onChange={(e) => p.onBrief(e.target.value)}
          onKeyDown={handleBriefKey}
          className={`${FIELD} resize-y font-mono`}
        />
      </label>

      <div className="grid grid-cols-1 @md:grid-cols-2 gap-2">
        <label className="flex flex-col gap-1 min-w-0">
          <span className={LABEL}>{t('newtab.headless.directory')}</span>
          <span className="flex items-center gap-1.5 min-w-0">
            <select
              data-testid="headless-root"
              value={p.root}
              disabled={locked}
              onChange={(e) => p.onRoot(e.target.value)}
              className={`${FIELD} font-mono min-w-0 flex-1`}
            >
              {p.caps.roots.map((r) => <option key={r.path} value={r.path}>{r.path}</option>)}
            </select>
            {rootKind && (
              <span data-testid="headless-root-kind"
                className="shrink-0 px-1.5 py-0.5 rounded text-[10px] uppercase tracking-wide bg-surface-primary border border-border-subtle text-text-muted">
                {rootKind}
              </span>
            )}
          </span>
        </label>

        <label className="flex flex-col gap-1 min-w-0">
          <span className={LABEL}>{t('newtab.headless.subpath')}</span>
          <input
            data-testid="headless-subpath"
            value={p.sub}
            disabled={locked}
            spellCheck={false}
            placeholder={t('newtab.headless.subpath_placeholder')}
            onChange={(e) => p.onSub(e.target.value)}
            className={`${FIELD} font-mono`}
          />
          {!p.subVerdict.ok && (
            <span data-testid="headless-subpath-error" data-reason={p.subVerdict.reason} className="text-xs text-red-400">
              {t(`newtab.headless.subpath_error.${p.subVerdict.reason}`)}
            </span>
          )}
        </label>
      </div>

      <label className="flex flex-col gap-1">
        <span className={LABEL}>{t('newtab.headless.profile')}</span>
        <span className="flex items-center gap-2">
          <select
            data-testid="headless-profile"
            value={p.profile}
            disabled={locked}
            onChange={(e) => p.onProfile(e.target.value)}
            className={`${FIELD} w-auto`}
          >
            {p.caps.sandbox_profiles.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
          <span data-testid="headless-max-profile" className="text-xs text-text-muted">
            {t('newtab.headless.max_profile', { profile: p.caps.sandbox_max_profile })}
          </span>
        </span>
      </label>

      {p.error && <p data-testid="headless-error" className="text-xs text-red-400">{p.error}</p>}

      <div className="flex justify-end">
        <button
          type="submit"
          data-testid="headless-submit"
          disabled={!p.canSubmit}
          className="flex items-center gap-1.5 px-3 py-1 rounded text-sm bg-accent text-white hover:bg-accent-hover cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <Lightning size={14} className={p.busy ? 'animate-pulse' : ''} />
          {t('newtab.headless.submit')}
        </button>
      </div>
    </form>
  )
}
