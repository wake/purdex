// spa/src/components/deck/search-scope.ts — the namespace of the search units drawn below it (#2665): an unfolded subagent's steps carry ids of
// their own that a main-turn item or another agent may share, so each is drawn (and indexed) under its task line's scope.
import { createContext } from 'react'

export const SearchScopeContext = createContext('')
export const subagentScope = (stepId: string): string => `${stepId}>`
