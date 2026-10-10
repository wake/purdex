// The right panel (U3 plan D10) from the golden fixtures: a chain (not the whole turn), a full output, a subagent; Esc / ✕;
// the width rule; and the open panel + its scroll across a real tab switch (CLAUDE.md tab-hosted rule).
import type { ReactNode } from 'react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { FoldContext } from '../room/fold-context'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { clearAllPanels, closePanel, openPanel, panelWidth, readPanel } from '../../lib/conversations/panel-memory'
import { forgetFolds, usePaneFoldStore } from '../../lib/conversations/fold-memory'
import { turnRows } from '../../lib/conversations/turn-row'
import type { ConversationItem, StepItem } from '../../lib/conversations/types'
import { SessionRightPanel } from './SessionRightPanel'
import pluginSubmit from '../../../../testdata/conversation/v1/cc-transcript/plugin-submit/expected.json'
import outputCaps from '../../../../testdata/conversation/v1/cc-transcript/output-caps/expected.json'
import subagent from '../../../../testdata/conversation/v1/cc-transcript/subagent/expected.json'
import subChild from '../../../../testdata/conversation/v1/cc-transcript/subagent/children/a7a639d97d57c6f43.expected.json'

const PANE = 'panel-test'
type Fx = { conversation: { turns: Array<{ id: string; index: number; items: unknown[] }> } }
const turnsOf = (f: unknown) => (f as Fx).conversation.turns.map((t) => ({ ...t, items: t.items.map((it, index) => ({ ...(it as object), index }) as ConversationItem) }))
const Fold = ({ children }: { children: ReactNode }) => <FoldContext.Provider value={usePaneFoldStore(PANE)}>{children}</FoldContext.Provider>
const mount = (turns: ReturnType<typeof turnsOf>, active = true) => render(<Fold><SessionRightPanel paneKey={PANE} turns={turns} active={active} /></Fold>)

// The turn of plugin-submit with two work chains (a step, then text, then another step).
const TWO = 'f3236e8d-7531-41bf-ac48-9950b41aa539'
const twoTurns = () => turnsOf(pluginSubmit)
const runsOfTwo = () => turnRows(twoTurns().find((t) => t.id === TWO)!).runs

const bigStep = (): StepItem & { output: NonNullable<StepItem['output']> } => {
  for (const t of turnsOf(outputCaps)) for (const i of t.items) {
    if (i.type === 'step' && (i as StepItem).output && (i as StepItem).output!.text.length > 500) return i as StepItem & { output: NonNullable<StepItem['output']> }
  }
  throw new Error('fixture step missing')
}

beforeEach(() => { cleanup(); clearAllPanels(); forgetFolds(PANE) })

describe('content', () => {
  it('a chain shows only that chain\'s steps, and its header names the turn and the position', () => {
    const runs = runsOfTwo()
    expect(runs.length).toBeGreaterThanOrEqual(2)
    openPanel(PANE, { kind: 'chain', turnId: TWO, firstStepId: runs[1].stepIds[0] })
    mount(twoTurns())
    const body = screen.getByTestId('panel-chain')
    const shown = within(body).getAllByTestId(/deck-step-(line|card)|deck-step-task/)
    expect(shown).toHaveLength(runs[1].steps.length)
    expect(screen.getByTestId('panel-title')).toHaveTextContent(`Turn ${twoTurns().find((t) => t.id === TWO)!.index + 1} · work 2 of ${runs.length}`)
  })

  it('a different chain of the same turn shows different steps (it is the chain, not the turn)', () => {
    const runs = runsOfTwo()
    openPanel(PANE, { kind: 'chain', turnId: TWO, firstStepId: runs[0].stepIds[0] })
    mount(twoTurns())
    expect(screen.getByTestId('panel-title')).toHaveTextContent(/work 1 of/)
    const first = within(screen.getByTestId('panel-chain')).getAllByTestId(/deck-step-(line|card)|deck-step-task/).length
    expect(first).toBe(runs[0].steps.length)
  })

  it('a full output shows every line the step has, not the deck\'s tail', () => {
    const step = bigStep()
    openPanel(PANE, { kind: 'output', stepId: step.id })
    mount(turnsOf(outputCaps))
    expect(screen.getByTestId('panel-output-text').textContent).toBe(step.output!.text)
  })

  it('a subagent shows its own steps', () => {
    const turns = turnsOf(subagent)
    const step = turns[0].items.find((i): i is StepItem => i.type === 'step')!
    step.children = (subChild as { items: StepItem['children'] }).items
    openPanel(PANE, { kind: 'subagent', stepId: step.id })
    mount(turns)
    expect(screen.getByTestId('panel-subagent')).toBeInTheDocument()
    expect(screen.getByTestId('panel-title')).toHaveTextContent('Subagent')
    expect(within(screen.getByTestId('panel-subagent')).getAllByTestId(/deck-step/).length).toBeGreaterThan(0)
  })

  it('says so when what it pointed at is gone', () => {
    openPanel(PANE, { kind: 'chain', turnId: 'no-such-turn', firstStepId: 'x' })
    mount(twoTurns())
    expect(screen.getByTestId('panel-gone')).toBeInTheDocument()
  })

  it('opening 「顯示全部」 from a chain keeps a way back', () => {
    const step = bigStep()
    const turn = turnsOf(outputCaps).find((t) => t.items.some((i) => i.id === step.id))!
    openPanel(PANE, { kind: 'chain', turnId: turn.id, firstStepId: turnRows(turn).runs.find((r) => r.stepIds.includes(step.id))!.stepIds[0] })
    mount(turnsOf(outputCaps))
    act(() => openPanel(PANE, { kind: 'output', stepId: step.id }, { keepBack: true }))
    expect(screen.getByTestId('panel-output')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('panel-back'))
    expect(screen.getByTestId('panel-chain')).toBeInTheDocument()
  })
})

describe('closing and width', () => {
  const open = () => openPanel(PANE, { kind: 'chain', turnId: TWO, firstStepId: runsOfTwo()[0].stepIds[0] })

  it('✕ closes it', () => {
    open(); mount(twoTurns())
    fireEvent.click(screen.getByTestId('panel-close'))
    expect(screen.queryByTestId('session-right-panel')).toBeNull()
    expect(readPanel(PANE)).toBeUndefined()
  })

  it('Esc closes it', () => {
    open(); mount(twoTurns())
    fireEvent.keyDown(document.body, { key: 'Escape' })
    expect(screen.queryByTestId('session-right-panel')).toBeNull()
  })

  it('Esc typed in a text field is the field\'s, and another key does nothing', () => {
    open()
    render(<Fold><textarea data-testid="box" /><SessionRightPanel paneKey={PANE} turns={twoTurns()} /></Fold>)
    fireEvent.keyDown(screen.getByTestId('box'), { key: 'Escape' })
    fireEvent.keyDown(document.body, { key: 'a' })
    expect(screen.getByTestId('session-right-panel')).toBeInTheDocument()
  })

  it('a pane that is not in front does not close on Esc', () => {
    open(); mount(twoTurns(), false)
    fireEvent.keyDown(document.body, { key: 'Escape' })
    expect(screen.getByTestId('session-right-panel')).toBeInTheDocument()
  })

  it('is 42 % of the pane, never under 320 nor over 640 px', () => {
    expect(panelWidth(1000)).toBe(420)
    expect(panelWidth(500)).toBe(320)
    expect(panelWidth(2000)).toBe(640)
    open(); mount(twoTurns())
    const el = screen.getByTestId('session-right-panel')
    expect([el.style.width, el.style.minWidth, el.style.maxWidth]).toEqual(['42%', '320px', '640px'])
  })
})

// The real TabContent: the alive pool keeps nothing (keepAliveCount 0), so the pane unmounts when the tab is left.
const H = 'h'
function PanelPane({ pane }: PaneRendererProps) {
  return <Fold><SessionRightPanel paneKey={pane.id} turns={twoTurns()} /></Fold>
}
const Other = () => <div data-testid="other-tab" />
const paneTab: Tab = { ...createTab({ kind: 'execution', executionId: 'exc_1', host: H }), id: 't-panel' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const paneIdOf = (paneTab.layout as { pane: { id: string } }).pane.id

describe('panel across tab switches', () => {
  beforeEach(() => {
    clearModuleRegistry()
    registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: PanelPane }] })
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
    useUISettingsStore.setState({ keepAliveCount: 0 })
    useShownHostsStore.setState({ ids: [H] })
    useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
    vi.restoreAllMocks()
  })

  it('is still open, on the same chain and at the same scroll when the reader comes back', () => {
    const all = [paneTab, dashTab]
    const runs = runsOfTwo()
    openPanel(paneIdOf, { kind: 'chain', turnId: TWO, firstStepId: runs[1].stepIds[0] })
    const { rerender } = render(<TabContent activeTab={paneTab} allTabs={all} />)
    const sc = screen.getByTestId('panel-scroll')
    sc.scrollTop = 123
    fireEvent.scroll(sc)

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('session-right-panel')).toBeNull() // really unmounted
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()

    rerender(<TabContent activeTab={paneTab} allTabs={all} />)
    expect(screen.getByTestId('panel-title')).toHaveTextContent(`work 2 of ${runs.length}`)
    expect((screen.getByTestId('panel-scroll') as HTMLElement).scrollTop).toBe(123)
  })

  it('stays closed when it was closed', () => {
    const all = [paneTab, dashTab]
    openPanel(paneIdOf, { kind: 'chain', turnId: TWO, firstStepId: runsOfTwo()[0].stepIds[0] })
    const { rerender } = render(<TabContent activeTab={paneTab} allTabs={all} />)
    act(() => closePanel(paneIdOf))
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    rerender(<TabContent activeTab={paneTab} allTabs={all} />)
    expect(screen.queryByTestId('session-right-panel')).toBeNull()
  })
})
