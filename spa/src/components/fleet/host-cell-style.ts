// spa/src/components/fleet/host-cell-style.ts — what the wide cell and the narrow block share: how far a host that is not ok is dimmed, and the count badge's colours.
/** How far a host is dimmed: wwd_down is lit as ever, host_down 40 %, unknown 60 % (M17–M19, M16). */
export const DIM = { ok: '', wwd_down: '', host_down: 'opacity-40', unknown: 'opacity-60' } as const
export const COUNT_TONE = { neutral: 'bg-text-muted text-white', warn: 'bg-status-warning text-text-inverse', error: 'bg-status-error text-white' } as const
