// spa/src/components/settings/personas/PersonaCredits.tsx — who drew the avatar styles the page offers, and under what
// licence (name pool v2 spec §9.1). Micah is CC BY 4.0 and must be credited; the others are CC0 and listed as courtesy.
import { useI18nStore } from '../../../stores/useI18nStore'
import { AVATAR_CREDITS } from '../../../lib/names/persona-credits'

const LINK = 'underline hover:text-text-primary'

export function PersonaCredits() {
  const t = useI18nStore((s) => s.t)
  return (
    <footer className="mt-6 border-t border-border-default pt-3 text-xs text-text-secondary" data-testid="persona-credits">
      <p>{t('settings.personas.credits.title')}</p>
      <ul className="mt-1 flex flex-col gap-0.5">
        {AVATAR_CREDITS.map((c) => (
          <li key={c.style} data-testid={`persona-credit-${c.style}`}>
            {c.label} — <a className={LINK} href={c.artistUrl} target="_blank" rel="noopener noreferrer">{c.artist}</a>
            {' · '}
            <a className={LINK} href={c.licenseUrl} target="_blank" rel="noopener noreferrer">{c.license}</a>
          </li>
        ))}
      </ul>
    </footer>
  )
}
