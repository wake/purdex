// spa/src/components/watch/host/LookSettings.tsx — the 設定 tab's 側欄顯示 section (monitoring tabs spec M9, M51, W7; plan MON-9a-3): the colour, icon and indicator of the host's cell on the sidebar, with a preview of that cell that follows the writes.
// Colour and icon go where the host's look lives (`writePoolHostLook`): a host with a daemon, into the Purdex host's look, which the tab badge reads too; a host without one, into the pool hosts store. The indicator is always the pool store's.
// The colour mode shown (console / terminal / execution) is the reader's own choice and is kept per host in `lib/watch/view-memory`, since this tab unmounts when the reader leaves it.
import { useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { usePoolHostsStore, INDICATOR_ITEMS, type IndicatorItem, type IndicatorShape, type PoolHostIndicator } from '../../../stores/usePoolHostsStore'
import { withColorLayer, withIcon } from '../../../stores/useHostStore'
import type { HostLookEntry } from '../../../stores/useHostLookStore'
import type { HostColorMode } from '../../../lib/host-color'
import { writePoolHostLook } from '../../../lib/pool/host-looks'
import { cellOf } from '../../../lib/pool/cell-model'
import { useMonitorHosts, type MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import { readLookMode, writeLookMode } from '../../../lib/watch/view-memory'
import { HostCell } from '../../fleet/HostCell'
import { HostColorPicker } from '../../hosts/HostColorPicker'
import { HostIconPicker } from '../../hosts/HostIconPicker'
import { Section } from '../../hosts/form-fields'
import { SegmentControl } from '../../settings/SegmentControl'
import { SettingItem } from '../../settings/SettingItem'

const SHAPES: readonly IndicatorShape[] = ['ring', 'bar', 'bars']
/** M51's default: a ring of the overall load. */
export const DEFAULT_INDICATOR: PoolHostIndicator = { shape: 'ring', data: ['load'] }
const SELECT = 'px-2 py-1 rounded border border-border-default bg-surface-primary text-xs text-text-primary'
const BTN = 'px-2 py-1 rounded bg-surface-tertiary text-text-secondary text-xs cursor-pointer'
const noop = () => {}

/** The indicator with its shape changed. To two bars, the left stays the item it was; the overall load (not a bar's item) becomes CPU, and the right is the memory pressure (the design's wireSettings). */
function withShape(cur: PoolHostIndicator, shape: IndicatorShape): PoolHostIndicator {
  if (cur.shape === shape) return cur
  if (shape !== 'bars') return { shape, data: [cur.data[0]] }
  const left: IndicatorItem = cur.data[0] === 'load' ? 'cpu' : cur.data[0]
  return { shape, data: [left, left === 'pressure' ? 'cpu' : 'pressure'] }
}

/** The indicator with the item of one bar changed; the two bars never read the same item. */
function withItem(cur: PoolHostIndicator, index: 0 | 1, item: IndicatorItem): PoolHostIndicator {
  if (cur.shape !== 'bars') return { shape: cur.shape, data: [item] }
  const other: IndicatorItem = item === cur.data[index === 0 ? 1 : 0] ? (item === 'cpu' ? 'pressure' : 'cpu') : cur.data[index === 0 ? 1 : 0]
  return { shape: 'bars', data: index === 0 ? [item, other] : [other, item] }
}

export function LookSettings({ host }: { host: MonitorHost }) {
  const t = useI18nStore((s) => s.t)
  const listed = useMonitorHosts()
  const [mode, setModeState] = useState<HostColorMode>(() => readLookMode(host.hostId))
  const setMode = (m: HostColorMode) => { writeLookMode(host.hostId, m); setModeState(m) }
  const look = host.look
  const indicator = look.indicator
  const write = (next: PoolHostIndicator) => writePoolHostLook(host.hostId, { indicator: next })
  const items = INDICATOR_ITEMS.map((i) => ({ value: i, label: t(`watch.look.item.${i}`) }))
  const itemSelect = (index: 0 | 1) => (
    <select data-testid={`look-data-${index}`} aria-label={t(index === 0 ? (indicator.shape === 'bars' ? 'watch.look.left' : 'watch.look.data') : 'watch.look.right')} className={SELECT}
      value={indicator.data[index] ?? 'pressure'} onChange={(e) => write(withItem(indicator, index, e.target.value as IndicatorItem))}>
      {items.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  )

  return (
    <Section title={t('watch.look.title')}>
      <div data-testid="look-settings">
        {host.daemonHostId !== null && <p data-testid="look-daemon-note" className="mb-2 text-xs text-text-muted">{t('watch.look.daemon_note')}</p>}
        <SettingItem label={t('watch.look.preview')}>
          <div data-testid="look-preview" className="w-64"><HostCell view={cellOf(host)} variant="wide" isActive={false} onOpen={noop} /></div>
        </SettingItem>
        <div className="py-2">
          <div className="text-xs text-text-secondary mb-1">{t('hosts.color.label')}</div>
          <HostColorPicker colors={look.colors} mode={mode} onModeChange={setMode}
            onSetLayer={(m, layer, value) => {
              const next = withColorLayer({ colors: look.colors } as HostLookEntry, m, layer, value)
              if (next) writePoolHostLook(host.hostId, { colors: next.colors ?? null })
            }}
            onClearMode={(m) => {
              const next = withColorLayer({ colors: look.colors } as HostLookEntry, m, 'main', null)
              if (next) writePoolHostLook(host.hostId, { colors: next.colors ?? null })
            }} />
        </div>
        <div className="py-2">
          <div className="text-xs text-text-secondary mb-1">{t('hosts.icon.label')}</div>
          <HostIconPicker icon={look.icon} weight={look.iconWeight}
            onChange={(icon, weight) => {
              const next = withIcon({ icon: look.icon, iconWeight: look.iconWeight } as HostLookEntry, icon, weight)
              if (next) writePoolHostLook(host.hostId, { icon: next.icon ?? null, iconWeight: next.iconWeight ?? null })
            }} />
        </div>
        <SettingItem label={t('watch.look.indicator')} description={t('watch.look.indicator_d')}>
          <div className="flex flex-col items-end gap-2">
            <SegmentControl options={SHAPES.map((s) => ({ value: s, label: t(`watch.look.shape.${s}`) }))} value={indicator.shape} onChange={(s) => write(withShape(indicator, s))} />
            <span className="flex items-center gap-2">
              {itemSelect(0)}
              {indicator.shape === 'bars' && itemSelect(1)}
            </span>
            <span className="flex items-center gap-2">
              <button type="button" data-testid="look-apply-all" className={BTN}
                onClick={() => usePoolHostsStore.getState().setIndicatorForAll(listed.map((h) => h.hostId), indicator)}>{t('watch.look.apply_all', { n: listed.length })}</button>
              <button type="button" data-testid="look-reset" className={BTN} onClick={() => write(DEFAULT_INDICATOR)}>{t('watch.look.reset')}</button>
            </span>
          </div>
        </SettingItem>
      </div>
    </Section>
  )
}
