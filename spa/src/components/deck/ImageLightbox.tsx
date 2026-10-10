// spa/src/components/deck/ImageLightbox.tsx — the original image over the page. Esc or a click on the backdrop closes it; focus
// goes to the close button while it is open (Tab stays there) and the caller puts it back where it was.
import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { X } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'

export function ImageLightbox({ src, name, onClose }: { src: string; name: string; onClose: () => void }) {
  const t = useI18nStore((s) => s.t)
  const closeBtn = useRef<HTMLButtonElement>(null)
  const onCloseRef = useRef(onClose)
  useEffect(() => { onCloseRef.current = onClose })
  useEffect(() => {
    closeBtn.current?.focus()
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { e.stopPropagation(); onCloseRef.current() }
      else if (e.key === 'Tab') { e.preventDefault(); closeBtn.current?.focus() }
    }
    document.addEventListener('keydown', key, true)
    return () => document.removeEventListener('keydown', key, true)
  }, [])
  return createPortal(
    <div
      data-testid="image-lightbox"
      role="dialog"
      aria-modal="true"
      aria-label={name}
      onClick={onClose}
      className="fixed inset-0 z-[1100] flex items-center justify-center bg-black/80 p-6"
    >
      <img src={src} alt={name} onClick={(e) => e.stopPropagation()} className="max-h-full max-w-full rounded object-contain" />
      <button
        ref={closeBtn}
        type="button"
        aria-label={t('deck.attach.lightbox_close')}
        title={t('deck.attach.lightbox_close')}
        onClick={(e) => { e.stopPropagation(); onClose() }}
        className="absolute right-4 top-4 flex h-9 w-9 items-center justify-center rounded-full bg-black/60 text-white hover:bg-black/80 focus-visible:outline focus-visible:outline-2 focus-visible:outline-white"
      ><X size={18} /></button>
    </div>,
    document.body,
  )
}
