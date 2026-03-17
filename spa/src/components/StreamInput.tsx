// spa/src/components/StreamInput.tsx
import { useState } from 'react'
import { PaperPlaneRight } from '@phosphor-icons/react'

interface Props {
  onSend: (text: string) => void
  disabled?: boolean
  placeholder?: string
}

export default function StreamInput({ onSend, disabled = false, placeholder = 'Type a message…' }: Props) {
  const [value, setValue] = useState('')

  function send() {
    const trimmed = value.trim()
    if (!trimmed) return
    onSend(trimmed)
    setValue('')
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      send()
    }
  }

  return (
    <div className="flex items-center gap-2 px-3 py-2 border-t border-gray-800 bg-gray-950">
      <input
        type="text"
        role="textbox"
        value={value}
        onChange={e => setValue(e.target.value)}
        onKeyDown={handleKeyDown}
        disabled={disabled}
        placeholder={placeholder}
        className="flex-1 bg-gray-800 text-gray-100 placeholder-gray-500 rounded-lg px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-blue-500 disabled:opacity-40 disabled:cursor-not-allowed"
      />
      <button
        data-testid="send-btn"
        onClick={send}
        disabled={disabled}
        className="flex-shrink-0 w-8 h-8 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed flex items-center justify-center cursor-pointer transition-colors"
      >
        <PaperPlaneRight size={15} weight="fill" className="text-white" />
      </button>
    </div>
  )
}
