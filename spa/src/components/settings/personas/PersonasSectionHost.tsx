// spa/src/components/settings/personas/PersonasSectionHost.tsx — what Settings › Personas registers. The page itself is
// loaded only when it is opened (`lazy`), so it, and DiceBear behind it, stay out of the main chunk (plan P3).
import { lazy, Suspense } from 'react'

const PersonasSection = lazy(() => import('./PersonasSection').then((m) => ({ default: m.PersonasSection })))

export function PersonasSectionHost() {
  return (
    <Suspense fallback={<div className="text-xs text-text-muted">…</div>}>
      <PersonasSection />
    </Suspense>
  )
}
