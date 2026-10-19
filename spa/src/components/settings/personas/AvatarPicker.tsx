// spa/src/components/settings/personas/AvatarPicker.tsx — chooses a persona's avatar (name pool v2 spec §9.1): a source
// (initials, one of the DiceBear styles, a photo address, an uploaded file), its seed and options, and the image drawn from
// them. It draws only when the user changes something, never on mount, so opening a persona leaves its image alone; the
// recipe and the 128 px image go out together, once the draw succeeded (`onChange`).
//
// A DiceBear change is drawn after a short pause, a photo only when "Fetch" is pressed (it is a network request), an
// uploaded file as soon as it is chosen. A late answer from an older draw is dropped.
import { useEffect, useRef, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { AVATAR_CREDITS } from '../../../lib/names/persona-credits'
import { DICEBEAR_STYLES, PersonaDrawError, drawRecipe, type DicebearStyle } from '../../../lib/names/persona-draw'
import { recallAvatarOptionsText, rememberAvatarOptionsText } from '../../../lib/names/avatar-options-memory'
import type { PersonaAvatarRecipe } from '../../../lib/names/persona-types'

const INPUT =
  'w-full rounded-md border border-border-default bg-surface-primary px-2 py-1 text-xs text-text-primary disabled:opacity-50'
const BTN =
  'shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'
const DEBOUNCE_MS = 400

interface Props {
  /** The persona this picker belongs to (its code, `''` while a new one is made): the key of the options memory. */
  slot: string
  recipe: PersonaAvatarRecipe
  disabled: boolean
  /** The recipe and the image drawn from it; `''` for no image. */
  onChange: (recipe: PersonaAvatarRecipe, avatarData: string) => void
}

const isStyle = (s: string): s is DicebearStyle => (DICEBEAR_STYLES as readonly string[]).includes(s)

/** The options as an object, `undefined` for empty text; `null` when the text is not a JSON object. */
function parseOptions(text: string): Record<string, unknown> | undefined | null {
  if (text.trim() === '') return undefined
  try {
    const v: unknown = JSON.parse(text)
    return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null
  } catch {
    return null
  }
}

function recipeOf(source: string, seed: string, options: Record<string, unknown> | undefined, url: string): PersonaAvatarRecipe {
  if (source === 'photo') return { source, url }
  if (source === 'upload') return { source }
  return { source, ...(seed !== '' ? { seed } : {}), ...(options && Object.keys(options).length > 0 ? { options } : {}) }
}

export function AvatarPicker({ slot, recipe, disabled, onChange }: Props) {
  const t = useI18nStore((s) => s.t)
  const [source, setSource] = useState(recipe.source)
  const [seed, setSeed] = useState(recipe.seed ?? '')
  const [url, setUrl] = useState(recipe.url ?? '')
  const [optionsText, setOptionsText] = useState(
    () => recallAvatarOptionsText(slot) ?? (recipe.options ? JSON.stringify(recipe.options) : ''),
  )
  const [error, setError] = useState<string | null>(null)
  const [drawing, setDrawing] = useState(false)
  const latest = useRef(0)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const optionsInvalid = parseOptions(optionsText) === null

  // Leaving (another persona was opened, the page was closed) drops what is still on its way: it was asked for something
  // that is no longer on screen, and applying it would put this picker's image into whatever is shown next.
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current)
    latest.current++
  }, [])

  const run = async (next: PersonaAvatarRecipe, deps?: { file?: Blob }) => {
    const mine = ++latest.current
    setDrawing(true)
    try {
      const data = await drawRecipe(next, deps)
      if (mine !== latest.current) return
      setError(null)
      onChange(next, data)
    } catch (e) {
      if (mine !== latest.current) return
      const code = e instanceof PersonaDrawError ? e.code : 'other'
      setError(t(`settings.personas.avatar.error.${code}`))
    } finally {
      if (mine === latest.current) setDrawing(false)
    }
  }

  /** A DiceBear recipe is drawn once the user stops typing; anything else waits for its own action. */
  const schedule = (nextSource: string, nextSeed: string, nextOptionsText: string) => {
    if (timer.current) clearTimeout(timer.current)
    const options = parseOptions(nextOptionsText)
    if (!isStyle(nextSource) || options === null) return
    timer.current = setTimeout(() => void run(recipeOf(nextSource, nextSeed, options, '')), DEBOUNCE_MS)
  }

  const changeSource = (next: string) => {
    setSource(next)
    setError(null)
    if (timer.current) clearTimeout(timer.current)
    if (next === '') {
      latest.current++ // an answer still on its way belongs to the source that was just left
      setDrawing(false)
      onChange({ source: '' }, '')
      return
    }
    schedule(next, seed, optionsText)
  }

  const changeSeed = (next: string) => { setSeed(next); schedule(source, next, optionsText) }

  const changeOptions = (next: string) => {
    setOptionsText(next)
    rememberAvatarOptionsText(slot, next)
    schedule(source, seed, next)
  }

  const chooseFile = (file: File | undefined) => {
    if (file) void run({ source: 'upload' }, { file })
  }

  return (
    <div className="flex flex-col gap-2" data-testid="avatar-picker">
      <label className="flex flex-col gap-1 text-xs text-text-secondary">
        <span>{t('settings.personas.avatar.source')}</span>
        <select data-testid="avatar-source" className={INPUT} value={source} disabled={disabled} onChange={(e) => changeSource(e.target.value)}>
          <option value="">{t('settings.personas.avatar.none')}</option>
          {AVATAR_CREDITS.map((c) => <option key={c.style} value={c.style}>{c.label}</option>)}
          <option value="photo">{t('settings.personas.avatar.photo')}</option>
          <option value="upload">{t('settings.personas.avatar.upload')}</option>
        </select>
      </label>

      {isStyle(source) && (
        <>
          <label className="flex flex-col gap-1 text-xs text-text-secondary">
            <span>{t('settings.personas.avatar.seed')}</span>
            <input data-testid="avatar-seed" className={INPUT} value={seed} disabled={disabled} onChange={(e) => changeSeed(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1 text-xs text-text-secondary">
            <span>{t('settings.personas.avatar.options')}</span>
            <textarea data-testid="avatar-options" rows={3} className={`${INPUT} font-mono`} value={optionsText} disabled={disabled} onChange={(e) => changeOptions(e.target.value)} />
          </label>
          {optionsInvalid && <p className="text-xs text-red-500" data-testid="avatar-options-error">{t('settings.personas.avatar.optionsInvalid')}</p>}
        </>
      )}

      {source === 'photo' && (
        <div className="flex items-end gap-2">
          <label className="flex min-w-0 flex-1 flex-col gap-1 text-xs text-text-secondary">
            <span>{t('settings.personas.avatar.url')}</span>
            <input data-testid="avatar-url" className={INPUT} value={url} disabled={disabled} onChange={(e) => setUrl(e.target.value)} />
          </label>
          <button type="button" className={BTN} data-testid="avatar-fetch" disabled={disabled || url.trim() === ''} onClick={() => void run(recipeOf('photo', '', undefined, url.trim()))}>
            {t('settings.personas.avatar.fetch')}
          </button>
        </div>
      )}

      {source === 'upload' && (
        <label className="flex flex-col gap-1 text-xs text-text-secondary">
          <span>{t('settings.personas.avatar.file')}</span>
          <input data-testid="avatar-file" type="file" accept="image/*" disabled={disabled} onChange={(e) => chooseFile(e.target.files?.[0])} />
        </label>
      )}

      {drawing && <p className="text-xs text-text-secondary" data-testid="avatar-drawing">{t('settings.personas.avatar.drawing')}</p>}
      {error && <p className="text-xs text-red-500" data-testid="avatar-error">{error}</p>}
    </div>
  )
}
