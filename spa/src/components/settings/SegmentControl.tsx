interface Option<T extends string> {
  value: T
  label: string
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
            onClick={() => { if (!isActive) onChange(opt.value) }}
            className={`px-4 py-1.5 text-xs border transition-colors cursor-pointer ${
              isActive
                ? 'bg-[#1e1e3e] border-[#7a6aaa] text-gray-200'
                : 'bg-transparent border-[#404040] text-gray-500 hover:text-gray-300 hover:border-gray-600'
            } ${isFirst ? 'rounded-l-md' : ''} ${isLast ? 'rounded-r-md' : ''}`}
          >
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}
