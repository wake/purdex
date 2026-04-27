import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { Plus, PencilSimple, Trash } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useQuickCommandStore } from '../../stores/useQuickCommandStore'
import { getBindingTargets, type QuickCommand } from '../../lib/quick-command-bindings'
import {
  QUICK_COMMAND_SLOTS,
  type QuickCommandSlotId,
} from '../../lib/quick-command-slots'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'

interface Props {
  ctx?: SettingsContextFor<'purdex'>
}

/**
 * Quick Commands settings (purdex scope).
 *
 * - List: capability order via store.global (no bindings filter — Settings
 *   shows everything user can edit).
 * - Edit dialog: focus trap, Esc-closes-and-returns-focus, multi-select chips
 *   for mount targets fed from QUICK_COMMAND_SLOTS.
 * - All keystrokes go through standard inputs; Space/Enter on the chip
 *   toggles the slot via native button semantics.
 */
export function QuickCommandsSettingsSection({ ctx: _ctx }: Props = {}) {
  const t = useI18nStore((s) => s.t)
  const commands = useQuickCommandStore((s) => s.global)
  const bindings = useQuickCommandStore((s) => s.bindings)
  const [editing, setEditing] = useState<QuickCommand | null>(null)
  const [creating, setCreating] = useState(false)
  const triggerRef = useRef<HTMLButtonElement | null>(null)

  const dialogOpen = creating || editing !== null

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-lg text-text-primary">{t('settings.quick_commands.title')}</h2>
        <button
          ref={triggerRef}
          type="button"
          onClick={() => {
            setEditing(null)
            setCreating(true)
          }}
          className="flex items-center gap-1 px-3 py-1.5 rounded text-xs text-text-primary border border-border-default hover:bg-surface-secondary cursor-pointer"
        >
          <Plus size={12} /> {t('settings.quick_commands.new')}
        </button>
      </div>

      {commands.length === 0 ? (
        <p className="text-sm text-text-muted py-8 text-center">
          {t('settings.quick_commands.empty')}
        </p>
      ) : (
        <ul className="space-y-2">
          {commands.map((cmd) => (
            <li
              key={cmd.id}
              data-testid={`qc-row-${cmd.id}`}
              className="border border-border-subtle rounded p-3 flex items-start gap-3"
            >
              <div className="flex-1 min-w-0">
                <div className="text-sm text-text-primary">{cmd.name}</div>
                <div className="text-xs text-text-muted truncate" title={cmd.command}>
                  {cmd.command}
                </div>
                <div className="mt-1 flex gap-1 flex-wrap">
                  {/* codex round-2 — own-property guard against capability ids
                      colliding with inherited Object.prototype methods (toString
                      / valueOf etc.); raw `bindings[cmd.id]` would resolve to
                      that function and `.map(...)` would crash the section. */}
                  {(getBindingTargets(bindings, cmd.id) ?? []).map((slot) => (
                    <span
                      key={slot}
                      className="text-[10px] text-text-secondary bg-surface-secondary px-1.5 py-0.5 rounded"
                    >
                      {slotLabel(slot, t)}
                    </span>
                  ))}
                </div>
              </div>
              <button
                type="button"
                onClick={() => {
                  setCreating(false)
                  setEditing(cmd)
                }}
                aria-label={t('common.edit')}
                className="p-1 text-text-muted hover:text-text-primary cursor-pointer"
              >
                <PencilSimple size={14} /> {t('common.edit')}
              </button>
              <button
                type="button"
                onClick={() => {
                  useQuickCommandStore.getState().removeCommand(cmd.id)
                }}
                aria-label={t('common.delete')}
                className="p-1 text-red-400 hover:text-red-300 cursor-pointer"
              >
                <Trash size={14} /> {t('common.delete')}
              </button>
            </li>
          ))}
        </ul>
      )}

      {dialogOpen && (
        <EditDialog
          initial={editing}
          onClose={() => {
            setCreating(false)
            setEditing(null)
            triggerRef.current?.focus()
          }}
          onSave={(cmd, targets) => {
            // Phase 1: only global capability is editable in UI; per-host
            // override deferred to a later phase.
            useQuickCommandStore.getState().addCommand(cmd)
            useQuickCommandStore.getState().setBinding(cmd.id, targets)
            setCreating(false)
            setEditing(null)
            triggerRef.current?.focus()
          }}
        />
      )}
    </div>
  )
}

function slotLabel(
  slot: QuickCommandSlotId,
  t: ReturnType<typeof useI18nStore.getState>['t'],
): string {
  switch (slot) {
    case QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS:
      return t('settings.quick_commands.slot.workspace')
    case QUICK_COMMAND_SLOTS.HOST_ACTIONS:
      return t('settings.quick_commands.slot.host')
    default:
      return slot
  }
}

interface DialogProps {
  initial: QuickCommand | null
  onClose: () => void
  onSave: (cmd: QuickCommand, targets: QuickCommandSlotId[]) => void
}

function EditDialog({ initial, onClose, onSave }: DialogProps) {
  const t = useI18nStore((s) => s.t)
  const allBindings = useQuickCommandStore((s) => s.bindings)
  const [name, setName] = useState(initial?.name ?? '')
  const [command, setCommand] = useState(initial?.command ?? '')
  const [icon, setIcon] = useState(initial?.icon ?? '')
  const [category, setCategory] = useState(initial?.category ?? '')
  const initialTargets = useMemo<QuickCommandSlotId[]>(
    // codex round-2 — same own-property guard as the list row.
    () => (initial ? getBindingTargets(allBindings, initial.id) ?? [] : []),
    [initial, allBindings],
  )
  const [targets, setTargets] = useState<QuickCommandSlotId[]>(initialTargets)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const firstInputRef = useRef<HTMLInputElement | null>(null)

  // Focus first input on mount
  useEffect(() => {
    firstInputRef.current?.focus()
  }, [])

  // Esc closes
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  // Focus trap inside dialog (basic Tab cycling)
  const onKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'Tab') return
    const root = dialogRef.current
    if (!root) return
    const focusable = root.querySelectorAll<HTMLElement>(
      'button, [href], input, textarea, select, [tabindex]:not([tabindex="-1"])',
    )
    if (focusable.length === 0) return
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      last.focus()
      e.preventDefault()
    } else if (!e.shiftKey && document.activeElement === last) {
      first.focus()
      e.preventDefault()
    }
  }, [])

  const toggleTarget = (slot: QuickCommandSlotId) => {
    setTargets((prev) =>
      prev.includes(slot) ? prev.filter((s) => s !== slot) : [...prev, slot],
    )
  }

  const handleSave = () => {
    const trimmedName = name.trim()
    const trimmedCmd = command.trim()
    if (!trimmedName || !trimmedCmd) return
    const id = initial?.id ?? `qc-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
    onSave(
      {
        id,
        name: trimmedName,
        command: trimmedCmd,
        icon: icon.trim() || undefined,
        category: category.trim() || undefined,
      },
      targets,
    )
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={t(initial ? 'settings.quick_commands.edit' : 'settings.quick_commands.new')}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onKeyDown={onKeyDown}
    >
      <div
        ref={dialogRef}
        className="w-[480px] bg-surface-primary border border-border-default rounded-lg p-4 space-y-3"
      >
        <h3 className="text-sm font-semibold">
          {t(initial ? 'settings.quick_commands.edit' : 'settings.quick_commands.new')}
        </h3>

        <label className="block text-xs text-text-secondary">
          {t('settings.quick_commands.name')}
          <input
            ref={firstInputRef}
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full mt-1 bg-surface-input border border-border-default rounded px-2 py-1 text-sm text-text-primary"
          />
        </label>

        <label className="block text-xs text-text-secondary">
          {t('settings.quick_commands.command')}
          <textarea
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            rows={3}
            className="w-full mt-1 bg-surface-input border border-border-default rounded px-2 py-1 text-sm font-mono text-text-primary"
          />
        </label>

        <div className="grid grid-cols-2 gap-2">
          <label className="block text-xs text-text-secondary">
            {t('settings.quick_commands.icon')}
            <input
              value={icon}
              onChange={(e) => setIcon(e.target.value)}
              className="w-full mt-1 bg-surface-input border border-border-default rounded px-2 py-1 text-sm text-text-primary"
            />
          </label>
          <label className="block text-xs text-text-secondary">
            {t('settings.quick_commands.category')}
            <input
              value={category}
              onChange={(e) => setCategory(e.target.value)}
              className="w-full mt-1 bg-surface-input border border-border-default rounded px-2 py-1 text-sm text-text-primary"
            />
          </label>
        </div>

        <fieldset className="border-t border-border-subtle pt-3">
          <legend className="text-xs text-text-secondary px-1">
            {t('settings.quick_commands.mount')}
          </legend>
          {/* codex round-1 C15 — roving focus across chips via ArrowLeft / ArrowRight */}
          <div
            className="flex gap-2 mt-1 flex-wrap"
            onKeyDown={(e) => {
              if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
              const buttons = Array.from(
                e.currentTarget.querySelectorAll<HTMLButtonElement>('button[type="button"]'),
              )
              const idx = buttons.indexOf(document.activeElement as HTMLButtonElement)
              if (idx === -1) return
              e.preventDefault()
              const dir = e.key === 'ArrowRight' ? 1 : -1
              const next = (idx + dir + buttons.length) % buttons.length
              buttons[next]?.focus()
            }}
          >
            {(Object.values(QUICK_COMMAND_SLOTS) as QuickCommandSlotId[]).map((slot) => {
              const active = targets.includes(slot)
              return (
                <button
                  key={slot}
                  type="button"
                  onClick={() => toggleTarget(slot)}
                  aria-pressed={active}
                  className={`px-2 py-1 text-xs rounded border cursor-pointer ${
                    active
                      ? 'bg-purple-500/20 text-text-primary border-purple-400'
                      : 'border-border-default text-text-secondary hover:text-text-primary'
                  }`}
                >
                  {slotLabel(slot, t)}
                </button>
              )
            })}
          </div>
        </fieldset>

        <div className="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onClose}
            className="px-3 py-1.5 text-xs text-text-secondary border border-border-default rounded cursor-pointer hover:text-text-primary"
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            onClick={handleSave}
            className="px-3 py-1.5 text-xs text-white bg-purple-500 rounded cursor-pointer hover:bg-purple-400"
          >
            {t('common.save')}
          </button>
        </div>
      </div>
    </div>
  )
}
