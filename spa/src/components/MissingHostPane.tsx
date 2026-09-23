import { LinkBreak } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  /** The reference exactly as stored — a wire id this device cannot resolve. */
  hostId: string
}

/**
 * The local-only state of a pane whose host this device does not have (host
 * ownership spec §3.2). Nothing is written: the reference stays verbatim so
 * the pane comes back to life when the host is added here, and other devices
 * that do have the host keep using it.
 */
export function MissingHostPane({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  return (
    <div
      data-testid="missing-host-pane"
      className="flex flex-col items-center justify-center h-full p-8 text-center"
    >
      <LinkBreak size={48} className="text-zinc-500 mb-4" />
      <h2 className="text-lg font-medium text-zinc-300 mb-1">{t('pane.missing_host.title', { host: hostId })}</h2>
      <p className="text-sm text-zinc-500">{t('pane.missing_host.desc')}</p>
    </div>
  )
}
