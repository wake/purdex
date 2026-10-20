// spa/src/components/overlay-dot.ts — the geometry of a status dot on a 16 px agent icon. The tab rows (TabStatusIndicator) and the team panel's seat light (TeamSeatLight) both draw from it, so they cannot drift apart.

/** The overlay dot's box, in px from the icon slot's top-right corner. */
export const OVERLAY_DOT = { size: 6, top: -1, right: -2 }
/** The overlay dot's ring width in px (its colour is the row's background). */
export const OVERLAY_RING = 1.5
/** The overlay error diamond's box (an error is drawn as a warning diamond, not a dot), in px from the icon slot's top-right corner. */
export const OVERLAY_ERROR = { size: 10, top: -2, right: -3 }
/** Where a seat light's box sits against the icon's top-right corner: the tab rows' place for the dot. */
export const SEAT_LIGHT_POSITION = { top: OVERLAY_DOT.top, right: OVERLAY_DOT.right }
