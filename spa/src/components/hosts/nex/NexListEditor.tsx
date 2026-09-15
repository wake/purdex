// spa/src/components/hosts/nex/NexListEditor.tsx — repeatable list of
// absolute-path strings (repo roots / service roots / PATH prepend entries)
// shared by NexConfigForm's three list fields.
import { Field } from '../form-fields'

export interface NexListEditorProps {
  label: string
  values: string[]
  onChange: (next: string[]) => void
  addLabel: string
  removeLabel: string
}

export default function NexListEditor({ label, values, onChange, addLabel, removeLabel }: NexListEditorProps) {
  return (
    <Field label={label}>
      <div className="space-y-1">
        {values.map((v, i) => (
          <div key={i} className="flex items-center gap-1">
            <input
              type="text"
              placeholder="/absolute/path"
              aria-label={`${label} ${i + 1}`}
              value={v}
              onChange={(e) => {
                const next = [...values]
                next[i] = e.target.value
                onChange(next)
              }}
              className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary w-full max-w-xs"
            />
            <button
              type="button"
              onClick={() => onChange(values.filter((_, j) => j !== i))}
              className="text-xs text-text-muted hover:text-red-400 cursor-pointer"
            >
              {removeLabel}
            </button>
          </div>
        ))}
        <button
          type="button"
          onClick={() => onChange([...values, ''])}
          className="text-xs text-accent hover:text-accent/80 cursor-pointer"
        >
          {addLabel}
        </button>
      </div>
    </Field>
  )
}
