import { EyeSlash } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { hostLabel, useHostLook } from '../lib/host-look'
import { hostRefOf } from '../lib/shown-hosts'
import type { PaneContent } from '../types/tab'

interface Props {
  content: PaneContent
}

/**
 * A pane whose host is hidden in this workbench (host ownership §1.2 rule 4; plan H2d-4, §0.21). Rendered by the gate
 * in `PaneLayoutRenderer`'s leaf branch INSTEAD of the pane's renderer: nothing below it mounts, so the pane opens no
 * connection. Nothing is written — the pane keeps its id and content, and showing the host again brings its renderer
 * back. The link opens the Hosts page on that host (a ref this device has no host for opens the page unselected).
 */
export function HostHiddenPane({ content }: Props) {
  const t = useI18nStore((s) => s.t)
  const ref = useHostStore((s) => hostRefOf(content, s.hostOrder)) ?? ''
  const host = hostLabel(ref, useHostLook(ref || null))

  const openHostPage = () => {
    const tabId = useTabStore.getState().openSingletonTab({ kind: 'hosts' })
    useWorkspaceStore.getState().insertTab(tabId)
    useTabStore.getState().setActiveTab(tabId)
    if (Object.hasOwn(useHostStore.getState().hosts, ref)) useHostStore.getState().setActiveHost(ref)
  }

  return (
    <div
      data-testid="host-hidden-pane"
      className="flex flex-col items-center justify-center h-full w-full p-8 text-center"
    >
      <EyeSlash size={48} className="text-zinc-500 mb-4" />
      <h2 className="text-lg font-medium text-zinc-300 mb-1">{t('pane.host_hidden.title')}</h2>
      <p className="text-sm text-zinc-500 mb-3">{t('pane.host_hidden.hint', { host })}</p>
      <button
        type="button"
        onClick={openHostPage}
        className="text-sm text-accent-base hover:underline cursor-pointer"
      >
        {t('pane.host_hidden.open_hosts')}
      </button>
    </div>
  )
}
