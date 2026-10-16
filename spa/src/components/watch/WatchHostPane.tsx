import type { PaneRendererProps } from '../../lib/module-registry'

export function WatchHostPane({ pane }: PaneRendererProps) {
  return <div className="h-full w-full" data-host={pane.content.kind === 'watch-host' ? pane.content.fleetHostId : ''} />
}
