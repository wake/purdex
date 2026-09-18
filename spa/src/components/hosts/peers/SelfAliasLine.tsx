// spa/src/components/hosts/peers/SelfAliasLine.tsx — the selected host's own
// identity line (`peers-self`) and its inline self-alias editor (self alias
// spec #1196 §4.3). The line keeps the D2 reading — `<name> · self alias:
// <alias> · <host_id>` — and adds: "(from host_id)" when the alias is derived,
// Edit → input / Save / Cancel, and Clear (back to the host_id default) only
// when there is a configured value to clear.
//
// Every write goes through the page's single flow runner under `selfKey`:
// the page lock holds while the PUT is out and the runner refreshes afterwards,
// so the counterparts' return lines show the drift a rename causes (S-4). The
// daemon's 400/409 text and the too-old verdict (S-5) land in the flow's
// `error`, which this line renders as `peers-self-error`; the page's orphan
// rule leaves `selfKey` to this line, so the text is painted once.
//
// Capability is known before the first click (codex F7): a GET that lacked
// `alias_source` is a daemon older than alpha.399, which would decode a PUT
// without the `alias` key and answer 200 as if nothing happened. That line
// gets the too-old sentence and no editor at all.
import { useState, type KeyboardEvent } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { updatePeerSettings, type PeerSettings } from '../../../lib/host-api'
import { selfKey, type BoundRunFlow, type FlowState } from './flow'

export interface SelfAliasProps {
  hostId: string
  hostName: string
  self: { host_id: string; self_alias: string; self_alias_source?: PeerSettings['alias_source'] }
  busy: boolean
  flow: FlowState | null
  /** Runs Save / Clear under `selfKey`; the runner holds the page lock and refreshes afterwards (spec §7.4). */
  runFlow: BoundRunFlow
}

const BTN = 'text-xs px-2 py-0.5 rounded cursor-pointer disabled:opacity-50 disabled:cursor-default font-sans'

/** The daemon's fallback (`config.PeerAlias()`): host_id up to the first ':', or all of it. */
const derivedAlias = (hostId: string): string => {
  const i = hostId.indexOf(':')
  return i > 0 ? hostId.slice(0, i) : hostId
}

export function SelfAliasLine({ hostId, hostName, self, busy, flow, runFlow }: SelfAliasProps) {
  const t = useI18nStore((s) => s.t)
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState('')
  const source = self.self_alias_source
  const mine = flow !== null && flow.key === selfKey
  const saving = mine && flow.running
  const error = mine && !flow.running ? flow.error : ''

  // One PUT with exactly the `alias` key (S-2: a missing key means unchanged,
  // so `deliver` is never sent from here). The answer is accepted by the same
  // rule the CLI applies (S-5, codex A2): a 200 without `alias_source` is an
  // old daemon that ignored the body; a Save must come back as exactly the
  // value sent with source `config`; a Clear must come back as a non-empty
  // alias with source `host_id`. Anything else is "not applied": the editor
  // stays open with the typed value and the line says what came back.
  // 400/409 throw and the runner turns them into the flow's error text (the
  // daemon's own sentence).
  const write = (alias: string) => {
    void runFlow(async () => {
      const r = await updatePeerSettings(hostId, { alias })
      if (!r.alias_source) return { error: t('peers.self_alias_too_old') }
      const applied = alias === ''
        ? r.alias_source === 'host_id' && r.alias !== ''
        : r.alias_source === 'config' && r.alias === alias
      if (!applied) return { error: t('peers.self_alias_not_applied', { alias: r.alias, source: r.alias_source }) }
      setEditing(false)
      return {}
    })
  }

  const edit = () => { setValue(self.self_alias); setEditing(true) }
  const cancel = () => setEditing(false)
  // Saving the same string while the alias is derived pins it into config
  // (spec §6 does exactly that); only a configured value equal to the input is a no-op.
  const unchanged = value === self.self_alias && source === 'config'
  const canSave = !busy && value !== '' && !unchanged
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' && canSave) write(value)
    if (e.key === 'Escape') cancel()
  }

  return (
    <div className="mb-4">
      {/* The selected host's own identity, labelled: the third of the three names (spec §2.1).
          It is what the return direction's entry on the counterpart must point at. */}
      <p data-testid="peers-self" className="text-xs text-text-muted font-mono flex items-center gap-2 flex-wrap">
        <span>
          {hostName} · {t('peers.self_alias_label')}: <span className="text-text-secondary">{self.self_alias}</span> · {self.host_id}
        </span>
        {source === 'host_id' && (
          <span data-testid="peers-self-from-host-id">{t('peers.self_alias_from_host_id')}</span>
        )}
        {source === undefined && (
          <span data-testid="peers-self-too-old" className="font-sans text-status-warning">{t('peers.self_alias_too_old')}</span>
        )}
        {source !== undefined && !editing && (
          <button type="button" data-testid="peers-self-edit" disabled={busy} onClick={edit}
            className={`${BTN} bg-surface-tertiary text-text-secondary hover:text-text-primary`}>{t('peers.self_alias_edit')}</button>
        )}
        {source === 'config' && (
          <>
            <button type="button" data-testid="peers-self-clear" disabled={busy} onClick={() => write('')}
              className={`${BTN} bg-surface-tertiary text-text-secondary hover:text-text-primary`}>{t('peers.self_alias_clear')}</button>
            {/* Spec §4.3: say what the alias WOULD become — clearing changes every address prefix at once. */}
            <span data-testid="peers-self-default-hint">{t('peers.self_alias_default_hint', { alias: derivedAlias(self.host_id) })}</span>
          </>
        )}
      </p>

      {editing && (
        <>
          <div className="mt-1.5 flex items-center gap-2 flex-wrap">
            <input data-testid="peers-self-input" type="text" value={value} disabled={busy} autoFocus spellCheck={false}
              onChange={(e) => setValue(e.target.value)} onKeyDown={onKey}
              className="text-xs font-mono px-2 py-1 rounded bg-surface-tertiary border border-border-subtle text-text-primary w-56 disabled:opacity-50" />
            <button type="button" data-testid="peers-self-save" disabled={!canSave} onClick={() => write(value)}
              className={`${BTN} bg-accent text-white`}>{saving ? t('peers.self_alias_saving') : t('peers.self_alias_save')}</button>
            <button type="button" data-testid="peers-self-cancel" disabled={busy} onClick={cancel}
              className={`${BTN} bg-surface-tertiary text-text-secondary hover:text-text-primary`}>{t('peers.self_alias_cancel')}</button>
          </div>
          {/* What a rename does and does not do (S-4), with the address prefix it would produce. */}
          <p className="text-xs text-text-muted mt-1">{t('peers.self_alias_note', { alias: value || self.self_alias })}</p>
        </>
      )}

      {error && (
        <p data-testid="peers-self-error" className="text-xs text-status-error whitespace-pre-wrap mt-1">{error}</p>
      )}
    </div>
  )
}
