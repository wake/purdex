interface Props {
  label: string
  checked: boolean
  onChange: (checked: boolean) => void
}

export function ToggleSwitch({ label, checked, onChange }: Props) {
  return (
    <button
      role="switch"
      aria-label={label}
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className={`w-9 h-5 rounded-full relative transition-all duration-150 cursor-pointer ${
        checked ? 'bg-accent' : 'bg-border-default'
      }`}
    >
      <span
        className={`absolute top-0.5 w-4 h-4 rounded-full transition-all duration-150 ${
          checked ? 'left-[18px] bg-white' : 'left-0.5 bg-text-secondary'
        }`}
      />
    </button>
  )
}
