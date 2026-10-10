// spa/src/lib/conversations/hover-place.ts — where the larger preview floats relative to its thumbnail: above it when it fits, else
// below, else clamped inside the viewport; always kept inside sideways. Pure, so the placement is testable without layout.

export interface Box { left: number; top: number; width: number; height: number }
export interface Size { w: number; h: number }
export interface Placement { left: number; top: number; side: 'above' | 'below' }

const GAP = 8
const MARGIN = 8

export function placeHoverCard(anchor: Box, card: Size, view: Size): Placement {
  const room = { above: anchor.top - GAP - MARGIN, below: view.h - (anchor.top + anchor.height) - GAP - MARGIN }
  const side: 'above' | 'below' = room.above >= card.h ? 'above' : room.below >= card.h ? 'below' : room.above >= room.below ? 'above' : 'below'
  const wanted = side === 'above' ? anchor.top - GAP - card.h : anchor.top + anchor.height + GAP
  // clamp inside the viewport (a card taller than the viewport starts at the top margin)
  const top = Math.max(MARGIN, Math.min(wanted, view.h - card.h - MARGIN))
  const left = Math.max(MARGIN, Math.min(anchor.left + anchor.width / 2 - card.w / 2, view.w - card.w - MARGIN))
  return { left, top, side }
}
