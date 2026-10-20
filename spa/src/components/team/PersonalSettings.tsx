// spa/src/components/team/PersonalSettings.tsx — the personal panel's settings block (task groups spec §7, TG-5c; design R16–R24): the group (move, take out, new group), the group's name and its colour. Always shown under the card.
// Moving, taking out and starting a group need the session's root (R23): until it is read the buttons stay off and say why. The panel never deletes a group (R22). The open list and the name draft are this component's own
// state — a tab switch drops them, as the sidebar's rename does. The group list is a disclosure of plain buttons (Tab reaches them in order; Escape closes it and gives the focus back to its button).
import { useEffect, useId, useRef, useState } from 'react'
import { Check, PencilSimple } from '@phosphor-icons/react'
import { TEAM_COLORS } from './team-display'
import { keepFocus } from './panel-focus'
import { useI18nStore } from '../../stores/useI18nStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import type { PersonalGroup } from '../../lib/task-groups/personal'
import type { PersonalActions } from './usePersonalActions'

interface Props {
  group: PersonalGroup
  rootKnown: boolean
  actions: PersonalActions
  /** Counts the requests to open the name field (a double-click on the capsule): each new value opens it once. */
  renameRequest: number
}

const ROW = 'flex items-center gap-2 px-2 min-h-7'
const LABEL = 'w-14 flex-shrink-0 text-[11px] text-text-muted'
const BTN = 'px-1.5 py-0.5 rounded text-[11px] border border-border-default text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer disabled:opacity-40 disabled:cursor-default disabled:hover:bg-transparent disabled:hover:text-text-secondary'

export function PersonalSettings({ group, rootKnown, actions, renameRequest }: Props) {
  const t = useI18nStore((s) => s.t)
  const order = useManualGroupsStore((s) => s.order)
  const groups = useManualGroupsStore((s) => s.groups)
  const [listOpen, setListOpen] = useState(false)
  // The name draft belongs to the group it was opened on: a draft whose group is not the one shown is nothing (it is never written to another).
  const [draftState, setDraft] = useState<{ groupId: string; value: string } | null>(null)
  const input = useRef<HTMLInputElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const listId = useId()
  const [wantEdit, setWantEdit] = useState<string | null>(null) // the id of a group just started from this session: its name field opens once the panel shows THAT group
  const [seenRequest, setSeenRequest] = useState(0)
  const manual = group.kind === 'manual' ? group : null
  const draft = draftState !== null && draftState.groupId === manual?.id ? draftState.value : null
  const openDraft = (value: string) => { if (manual !== null) setDraft({ groupId: manual.id, value }) }
  // A press on a button keeps the terminal's focus — unless a name draft is open: then the press is a click elsewhere, the field loses focus and the draft is dropped (R20).
  const press = (e: React.MouseEvent) => { if (draft === null) keepFocus(e) }
  const waiting = rootKnown ? undefined : t('taskgroups.err.no-root')

  // Adjusting state while rendering (no effect): a new rename request, or a group just started from this session, opens the name field.
  if (renameRequest !== seenRequest) {
    setSeenRequest(renameRequest)
    if (renameRequest > 0 && manual !== null) openDraft(manual.name)
  }
  if (wantEdit !== null && manual?.id === wantEdit) {
    setWantEdit(null)
    openDraft(manual.name)
  }
  useEffect(() => { if (draft !== null) input.current?.select() }, [draft === null]) // eslint-disable-line react-hooks/exhaustive-deps -- select once when the field opens

  const commit = () => {
    if (draft !== null && manual !== null && draft.trim() !== manual.name) actions.rename(manual.id, draft.trim())
    setDraft(null)
  }

  return (
    <div data-testid="personal-settings" className="border-t border-border-subtle py-1.5 flex flex-col gap-1">
      <div className={ROW}>
        <span className={LABEL}>{t('personal.settings.group')}</span>
        <span className="relative min-w-0 flex-1">
          <button
            type="button"
            ref={trigger}
            data-testid="personal-group-button"
            aria-controls={listOpen ? listId : undefined}
            aria-expanded={listOpen}
            disabled={!rootKnown || actions.busy}
            title={waiting}
            onMouseDown={press}
            onClick={() => setListOpen((v) => !v)}
            className={`${BTN} max-w-full truncate`}
          >
            {manual !== null ? manual.name : t('taskgroups.ungrouped')} ▾
          </button>
          {listOpen && (
            <ul id={listId} data-testid="personal-group-list" onKeyDown={(e) => { if (e.key === 'Escape') { setListOpen(false); trigger.current?.focus() } }} className="absolute z-30 left-0 top-full mt-1 min-w-40 max-h-60 overflow-y-auto rounded-md border border-border-default bg-surface-elevated shadow-xl py-1 text-[11.5px]">
              {order.flatMap((id) => (groups[id] ? [id] : [])).map((id) => (
                <li key={id}>
                  <button
                    type="button"
                    aria-pressed={manual?.id === id}
                    data-testid="personal-group-option"
                    onMouseDown={press}
                    onClick={() => { setListOpen(false); if (manual?.id !== id) void actions.move(id) }}
                    className="w-full flex items-center gap-1.5 px-2 py-1 text-left text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
                  >
                    <span className="w-3 flex-shrink-0">{manual?.id === id && <Check size={11} />}</span>
                    <span className="truncate">{groups[id].name}</span>
                  </button>
                </li>
              ))}
              <li className="border-t border-border-subtle mt-1 pt-1">
                <button
                  type="button"
                  data-testid="personal-group-new"
                  disabled={actions.busy}
                  onMouseDown={press}
                  onClick={() => { setListOpen(false); void actions.startGroup().then((id) => { if (id !== null) setWantEdit(id) }) }}
                  className="w-full px-2 py-1 text-left text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
                >
                  {t('personal.settings.new_group')}
                </button>
              </li>
            </ul>
          )}
        </span>
        <button
          type="button"
          data-testid="personal-take-out"
          disabled={!rootKnown || manual === null || actions.busy}
          title={waiting}
          onMouseDown={press}
          onClick={() => void actions.takeOut()}
          className={BTN}
        >
          {t('personal.settings.take_out')}
        </button>
      </div>
      {manual === null ? (
        <p data-testid="personal-ungrouped-hint" className="px-2 text-[11px] text-text-muted">{t('personal.settings.ungrouped_hint')}</p>
      ) : (
        <>
          <div className={ROW}>
            <span className={LABEL}>{t('personal.settings.name')}</span>
            {draft === null ? (
              <button
                type="button"
                data-testid="personal-name-edit"
                onMouseDown={press}
                onClick={() => openDraft(manual.name)}
                title={t('personal.settings.rename')}
                className="min-w-0 flex-1 flex items-center gap-1 text-left text-[12px] text-text-primary cursor-pointer"
              >
                <span className="truncate">{manual.name}</span>
                <PencilSimple size={11} className="flex-shrink-0 text-text-muted" />
              </button>
            ) : (
              <input
                ref={input}
                data-testid="personal-name-input"
                value={draft}
                onChange={(e) => openDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') commit()
                  else if (e.key === 'Escape') setDraft(null)
                }}
                onBlur={() => setDraft(null)}
                className="min-w-0 flex-1 px-1.5 py-0.5 rounded border border-border-default bg-surface-primary text-[12px] text-text-primary outline-none focus:border-accent"
              />
            )}
          </div>
          <div className={ROW}>
            <span className={LABEL}>{t('personal.settings.color')}</span>
            <span data-testid="personal-colors" className="flex items-center gap-1.5 flex-wrap">
              {TEAM_COLORS.map((c) => (
                <button
                  key={c}
                  type="button"
                  data-testid="personal-color"
                  data-color={c}
                  aria-pressed={manual.color?.toLowerCase() === c}
                  aria-label={c}
                  onMouseDown={press}
                  onClick={() => actions.recolor(manual.id, c)}
                  className={`w-4 h-4 rounded-full cursor-pointer ${manual.color?.toLowerCase() === c ? 'ring-2 ring-offset-1 ring-text-primary ring-offset-surface-elevated' : ''}`}
                  style={{ background: c }}
                />
              ))}
            </span>
          </div>
        </>
      )}
      {actions.notice !== null && <p data-testid="personal-notice" role="status" className="px-2 text-[11px] text-status-warning-text">{actions.notice}</p>}
    </div>
  )
}
