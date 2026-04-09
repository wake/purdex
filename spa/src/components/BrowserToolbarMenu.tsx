import { useEffect, useRef } from 'react'
import {
  ArrowSquareOut,
  Copy,
  ArrowSquareUpRight,
} from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'

interface MenuItem {
  label: string
  icon: React.ReactNode
  onClick: () => void
  show: boolean
}

interface BrowserToolbarMenuProps {
  onOpenExternal: () => void
  onCopyUrl: () => void
  onPopOut?: () => void
  onClose: () => void
}

export function BrowserToolbarMenu({
  onOpenExternal,
  onCopyUrl,
  onPopOut,
  onClose,
}: BrowserToolbarMenuProps) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose()
    }
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('mousedown', handleClick)
    document.addEventListener('keydown', handleKey)
    return () => {
      document.removeEventListener('mousedown', handleClick)
      document.removeEventListener('keydown', handleKey)
    }
  }, [onClose])

  const items: MenuItem[] = [
    {
      label: t('browser.open_external'),
      icon: <ArrowSquareOut size={14} />,
      onClick: () => { onOpenExternal(); onClose() },
      show: true,
    },
    {
      label: t('browser.copy_url'),
      icon: <Copy size={14} />,
      onClick: () => { onCopyUrl(); onClose() },
      show: true,
    },
    {
      label: t('browser.pop_out'),
      icon: <ArrowSquareUpRight size={14} />,
      onClick: () => { onPopOut?.(); onClose() },
      show: !!onPopOut,
    },
  ]

  return (
    <div
      ref={ref}
      className="absolute right-0 top-full mt-1 z-50 min-w-48 bg-surface-elevated border border-border-default rounded-lg shadow-xl py-1 text-xs"
    >
      {items.filter((i) => i.show).map((item) => (
        <button
          key={item.label}
          className="flex w-full items-center gap-2 px-3 py-1.5 text-text-primary cursor-pointer hover:bg-surface-hover transition-colors"
          onClick={item.onClick}
        >
          {item.icon}
          {item.label}
        </button>
      ))}
    </div>
  )
}
