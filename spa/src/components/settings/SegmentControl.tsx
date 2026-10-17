interface Option<T extends string> {
  value: T
  label: string
  disabled?: boolean
}

interface Props<T extends string> {
  options: Option<T>[]
  value: T
  onChange: (value: T) => void
}

export function SegmentControl<T extends string>({ options, value, onChange }: Props<T>) {
  return (
    <div className="flex">
      {options.map((opt, i) => {
        const isActive = opt.value === value
        const isFirst = i === 0
        const isLast = i === options.length - 1
        return (
          <button
            key={opt.value}
            type="button"
            aria-pressed={isActive}
            disabled={opt.disabled}
            onClick={() => { if (!isActive) onChange(opt.value) }}
            className={`px-4 py-1.5 text-xs border transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed ${
              isActive
                ? 'bg-surface-elevated border-border-active text-text-primary'
                : 'bg-transparent border-border-default text-text-muted enabled:hover:text-text-primary enabled:hover:border-text-muted'
            } ${isFirst ? 'rounded-l-md' : ''} ${isLast ? 'rounded-r-md' : ''}`}
          >
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}
