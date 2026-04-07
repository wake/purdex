import { useEffect, useRef, useState } from 'react'
import { CaretDown, CaretRight, Eye, EyeSlash, Check, X } from '@phosphor-icons/react'

/* ─── Collapsible section wrapper ─── */

export function Section({ title, defaultOpen = true, children }: { title: string; defaultOpen?: boolean; children: React.ReactNode }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="mb-6">
      <button
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="flex items-center gap-2 text-sm font-semibold text-text-primary mb-3 cursor-pointer"
      >
        {open ? <CaretDown size={12} /> : <CaretRight size={12} />}
        {title}
      </button>
      {open && children}
    </div>
  )
}

/* ─── Field row ─── */

export function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-4 py-2">
      <span className="text-xs text-text-secondary w-32 shrink-0">{label}</span>
      <div className="flex-1">{children}</div>
    </div>
  )
}

/* ─── Editable text field ─── */

export function EditableField({ label, value, onSave }: { label: string; value: string; onSave: (v: string) => void }) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value)
  const savedRef = useRef(false)

  useEffect(() => { setDraft(value) }, [value])
  useEffect(() => { if (editing) savedRef.current = false }, [editing])

  const save = () => {
    if (savedRef.current) return
    savedRef.current = true
    onSave(draft)
    setEditing(false)
  }

  if (!editing) {
    return (
      <Field label={label}>
        <span
          className="text-sm text-text-primary cursor-pointer hover:text-accent"
          onClick={() => setEditing(true)}
        >
          {value || '—'}
        </span>
      </Field>
    )
  }

  return (
    <Field label={label}>
      <input
        className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary w-full max-w-xs"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => {
          if (e.key === 'Enter') save()
          if (e.key === 'Escape') { setDraft(value); setEditing(false) }
        }}
        autoFocus
      />
    </Field>
  )
}

/* ─── Token field with validation ─── */

export function TokenField({ token, ip, port, onSave, t }: {
  token?: string
  ip: string
  port: number
  onSave: (token: string) => void
  t: (key: string) => string
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(token ?? '')
  const [visible, setVisible] = useState(false)
  const [validating, setValidating] = useState(false)
  const [error, setError] = useState('')

  const handleSave = async () => {
    if (draft === (token ?? '')) {
      setEditing(false)
      return
    }
    // Empty token: skip validation, just save
    if (!draft.trim()) {
      setError('')
      onSave(draft)
      setEditing(false)
      return
    }
    // Validate token by testing /api/sessions with it
    setValidating(true)
    setError('')
    try {
      const base = `http://${ip}:${port}`
      const headers: Record<string, string> = {}
      if (draft) headers['Authorization'] = `Bearer ${draft}`
      const res = await fetch(`${base}/api/sessions`, { headers })
      if (res.ok) {
        onSave(draft)
        setEditing(false)
        setVisible(false)
      } else if (res.status === 401) {
        setError(t('hosts.invalid_token'))
      } else {
        setError(`HTTP ${res.status}`)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t('hosts.connection_failed'))
    } finally {
      setValidating(false)
    }
  }

  const handleCancel = () => {
    setDraft(token ?? '')
    setEditing(false)
    setError('')
  }

  if (!editing) {
    return (
      <Field label={t('hosts.token')}>
        <span className="inline-flex items-center gap-2">
          <span className="text-sm text-text-muted font-mono">
            {token ? (visible ? token : '••••••••') : '—'}
          </span>
          {token && (
            <button
              onClick={() => setVisible(!visible)}
              aria-label={visible ? t('hosts.hide_token') : t('hosts.show_token')}
              className="text-text-muted hover:text-text-secondary cursor-pointer"
            >
              {visible ? <EyeSlash size={14} /> : <Eye size={14} />}
            </button>
          )}
          <button
            onClick={() => { setDraft(token ?? ''); setEditing(true) }}
            className="text-xs text-accent hover:text-accent/80 cursor-pointer"
          >
            {t('common.edit')}
          </button>
        </span>
      </Field>
    )
  }

  return (
    <Field label={t('hosts.token')}>
      <div className="space-y-1">
        <div className="flex items-center gap-1">
          <input
            type={visible ? 'text' : 'password'}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="purdex_..."
            className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary font-mono w-full max-w-xs"
            autoFocus
            onKeyDown={(e) => {
              if (e.key === 'Enter') handleSave()
              if (e.key === 'Escape') handleCancel()
            }}
          />
          <button
            onClick={() => setVisible(!visible)}
            aria-label={visible ? t('hosts.hide_token') : t('hosts.show_token')}
            className="text-text-muted hover:text-text-secondary cursor-pointer p-1"
          >
            {visible ? <EyeSlash size={14} /> : <Eye size={14} />}
          </button>
          <button
            onClick={handleSave}
            disabled={validating}
            aria-label={t('common.save')}
            className="text-green-400 hover:text-green-300 cursor-pointer p-1 disabled:opacity-50"
          >
            <Check size={14} />
          </button>
          <button
            onClick={handleCancel}
            aria-label={t('common.cancel')}
            className="text-text-muted hover:text-text-secondary cursor-pointer p-1"
          >
            <X size={14} />
          </button>
        </div>
        {validating && <p className="text-xs text-text-muted">{t('hosts.validating_token')}</p>}
        {error && <p className="text-xs text-red-400">{error}</p>}
        <p className="text-xs text-text-muted">{t('hosts.token_hint')}</p>
      </div>
    </Field>
  )
}
