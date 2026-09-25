// spa/src/components/room/RoomTurnGroup.tsx — one turn of the room transcript.
//
// The turn is a container and nothing more to look at: no border, no rule
// between turns, no per-turn duration or cost (spec Q1 — the user's call).
// Its one job on screen is the hover strip that folds everything inside it at
// once.
//
// It takes no list of fold keys. Every foldable block registers itself with
// the turn it sits in, read off `TurnIndexContext`, so "all" is whatever is
// mounted here — operations, their raw input and diff, thinking blocks, and
// whatever is added next — without the caller keeping a list that would
// silently fall behind (spec §3.2, codex plan review #6).
import type { ReactNode } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { TurnIndexContext, useFoldStore } from './fold-context'

export interface RoomTurnGroupProps {
  index: number
  children: ReactNode
}

const STRIP_BUTTON_CLASS = 'text-xs text-text-muted hover:text-text-primary cursor-pointer'

export default function RoomTurnGroup({ index, children }: RoomTurnGroupProps) {
  const t = useI18nStore((s) => s.t)
  const { setTurn } = useFoldStore()

  return (
    <section data-testid="room-turn" data-turn-index={index} className="group relative">
      {/*
        Hidden until the turn is hovered: the strip is a tool for the reader,
        not part of what the turn says. It sits over the turn's top-right
        corner rather than taking a row, so a hover never reflows the text.
      */}
      <div
        data-testid="turn-fold-strip"
        className="absolute right-0 top-0 flex gap-2 opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity"
      >
        <button
          type="button"
          data-testid="turn-expand-all"
          className={STRIP_BUTTON_CLASS}
          onClick={() => setTurn(index, true)}
        >
          {t('room.turn.expand_all')}
        </button>
        <button
          type="button"
          data-testid="turn-collapse-all"
          className={STRIP_BUTTON_CLASS}
          onClick={() => setTurn(index, false)}
        >
          {t('room.turn.collapse_all')}
        </button>
      </div>
      <TurnIndexContext.Provider value={index}>{children}</TurnIndexContext.Provider>
    </section>
  )
}
