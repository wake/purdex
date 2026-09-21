// spa/src/components/settings/profile/wizard/ProfileWizard.tsx — Settings › Profile › the wizard: how a user
// attaches a master without the dev hook (Profile Sync spec §4.9, decisions 10 and 12; P3 plan, P3d-3).
//
// ONE STEP AT A TIME, EACH CONFIRMED, IN THIS ORDER (decision 10) — `stop` only with a master attached:
//   stop       `detachMaster()`, the very call Stop sync makes. A host that was not told is SAID, and the wizard
//              waits for the user; the notice that stays (and its Try again) is `StopSyncControl`'s, below this.
//   sot        the host (the dev host by default; only a connected one can be chosen) and the profile on it —
//              an existing one, or a new one, created when the step is confirmed.
//   local      which local profile becomes the master. Choosing one that is not the master is a MOVE, done in
//              the last step; here it is only said.
//   direction  push / pull. A new profile — and one that holds nothing — offers push only. Pull shows what it
//              replaces (counts) and offers to keep a copy first: ticked by default, named after this device.
//   run        a summary, Start, then the sub-steps of wizard-run.ts. A failure stops the run where it is.
// There is no way to a step but through the one before it: the step list is text, and Next is the only door.
//
// NOTHING IS REMEMBERED. The state below is this component's; closing the wizard or leaving the page drops it,
// and what a run had already done stays done (a promote is a fact). Opened again, it starts from the state as
// it is then.
//
// EVERY PREMISE IS CHECKED AGAIN (`brokenPremise`, over wizard-run.ts's `brokenLocalPremise`) — on every store
// change and at every click that moves on: another window may have attached a master, deleted the chosen local
// profile, or the host may have gone. The wizard then returns to the latest step whose premises hold and says
// why. Not while a run is under way: there each primitive refuses for itself, and its refusal is the run's
// failure.
//
// START (AND EVERY RETRY) GOES THROUGH ONE DOOR: wizard-run.ts's `prepareRun` — this device's premises AND the
// SOT profile, re-listed, against the fingerprint the user was looking at when they chose it (`seen`, review
// F1). This file only presents what that door answers: a plan → the run; `profile-changed` → back to the
// direction step, the direction un-chosen, what is offered recomputed from what is there NOW; `profile-gone` →
// back to the profile step; `list-failed` → nothing runs, it is said, Start stays. "Pull is not offered" follows
// `seen.empty` — never a local "I created it" flag alone.
//
// A CREATE WHOSE OUTCOME IS NOT KNOWN (review F2) is wizard-run.ts's `createSotProfile`; this file keeps the two
// things it needs across presses: the BASELINE (the ids the host listed before this visit's first POST to it —
// null when the list had never been read) and for which host + name the next press must LOOK FIRST.
//
// THE RESULT IS ALSO A TOAST (`announceRun`, acceptance F6): a pull replaces the world this page lives in. The
// run's promise outlives the component — it announces, and sets state only while `alive`.
//
// WHAT IS SHOWN OF A FAILURE is a sentence chosen by the failure's class. Never a transport's message, an
// `Error.message` or a response body (wizard-run.ts closes the list for the run; `requestKey` for the requests).
import { useEffect, useRef, useState } from 'react'
import { ArrowsClockwise, CheckCircle, Circle, WarningCircle } from '@phosphor-icons/react'
import { useI18nStore } from '../../../../stores/useI18nStore'
import { selectDevHostId, useHostStore } from '../../../../stores/useHostStore'
import { MASTER_PROFILE_ID, normalizeLocalProfileName, useLocalProfilesStore } from '../../../../stores/useLocalProfilesStore'
import { useProfileStore, type SyncDirection } from '../../../../stores/useProfileStore'
import { ensureDefaultDeviceName } from '../../../../stores/useDeviceNameStore'
import { isClientIdPersisted } from '../../../../lib/client-identity'
import { readMasterWorld } from '../../../../lib/profile/master-world'
import { detachMaster, type DetachResult } from '../../../../lib/profile/start'
import { useSotProfiles } from '../useSotProfiles'
import { DirectionStep, LocalStep, SotStep, type SotChoice } from './WizardChoiceSteps'
import { announceRun, brokenLocalPremise, createSotProfile, offeredProfileName, prepareRun, runPlan, sotNow, subStepsOf, type CreateResult, type SotNow, type SubStepId, type SubStepState, type WizardDraft, type WizardPlan } from './wizard-run'
import { BTN, NOTICE, reasonKey, requestKey, useMasterWorldReason } from './wizard-shared'

type StepId = 'stop' | 'sot' | 'local' | 'direction' | 'run'
const ORDER: readonly StepId[] = ['stop', 'sot', 'local', 'direction', 'run']

type Refusal = 'client-id' | 'junk-epoch' | 'no-parked-master'
type NoticeReason = 'attached-elsewhere' | 'stopped-elsewhere' | 'host-gone' | 'host-offline' | 'local-gone' | 'profile-changed' | 'profile-emptied' | 'profile-gone' | 'create-maybe'
type PremiseReason = Extract<NoticeReason, 'attached-elsewhere' | 'stopped-elsewhere' | 'host-gone' | 'host-offline' | 'local-gone'>

/** Which step a broken premise sends the wizard back to. */
const PREMISE_STEP: Record<Exclude<PremiseReason, 'stopped-elsewhere'>, StepId> = { 'attached-elsewhere': 'stop', 'host-gone': 'sot', 'host-offline': 'sot', 'local-gone': 'local' }

/** Reasons no retry cures: the wizard starts over, from the state as it is by then. */
const FINAL: Record<SubStepId, ReadonlySet<string>> = {
  promote: new Set(['master-attached', 'not-found', 'bad-name']),
  save: new Set(['bad-name']),
  attach: new Set(['invalid-direction', 'client-id-not-persisted', 'unknown-host', 'invalid-profile-id', 'superseded', 'not-found']),
}

const masterAttached = (): boolean => {
  const s = useProfileStore.getState()
  return s.masterHostId !== null && s.masterProfileId !== null && s.masterEndpoint !== null
}
const hostConnected = (hostId: string): boolean => useHostStore.getState().runtime[hostId]?.status === 'connected'

/** Why the wizard cannot even begin. The two world states are the ones nothing in here can mend: a promote and
 *  the copy both refuse an unsettled world, and these two do not settle by waiting (master-world.ts). */
function refusalNow(): Refusal | null {
  if (!isClientIdPersisted()) return 'client-id'
  const read = readMasterWorld()
  if (!read.settled && (read.reason === 'junk-epoch' || read.reason === 'no-parked-master')) return read.reason
  return null
}

/** The dev host when it is connected, else the first connected host, else none. */
function defaultHostId(): string | null {
  const hosts = useHostStore.getState()
  const dev = selectDevHostId(hosts)
  if (dev !== null && hostConnected(dev)) return dev
  return hosts.hostOrder.find((id) => hosts.hosts[id] !== undefined && hostConnected(id)) ?? null
}

/** The EARLIEST premise of `step` that does not hold, read off the stores as they are this instant. */
function brokenPremise(step: StepId, hostId: string | null, localId: string): { step: StepId; reason: PremiseReason } | null {
  if (step === 'stop') return masterAttached() ? null : { step: 'sot', reason: 'stopped-elsewhere' }
  const at = ORDER.indexOf(step)
  // A step's premises are those of the choices made BEFORE it: the host from `local` on, the local profile from `direction` on.
  const reason = brokenLocalPremise(hostId, localId, { host: at > ORDER.indexOf('sot'), local: at > ORDER.indexOf('local') })
  return reason === null ? null : { step: PREMISE_STEP[reason], reason }
}

interface RunState {
  plan: WizardPlan
  states: SubStepState[]
  /** `checking`: `prepareRun` is asking the host; nothing has been done (or, on a retry, nothing more). */
  phase: 'idle' | 'checking' | 'running' | 'failed' | 'done'
  failure: { at: number; reason: string } | null
  /** The host could not be asked before the run (a request's class): nothing ran. */
  checkFailed: string | null
  /** The chosen local profile's name as it was when the run began: after the promote it is no local profile any more. */
  localName: string | null
}

export function ProfileWizard({ onClose }: { onClose: () => void }) {
  const t = useI18nStore((s) => s.t)
  const [refusal] = useState<Refusal | null>(refusalNow)
  const [hadMaster, setHadMaster] = useState(masterAttached)
  const [step, setStep] = useState<StepId>(() => (masterAttached() ? 'stop' : 'sot'))
  const [notice, setNotice] = useState<NoticeReason | null>(null)
  const [stopping, setStopping] = useState(false)
  const [stopResult, setStopResult] = useState<Exclude<DetachResult, { ok: true }> | null>(null)
  const [hostId, setHostId] = useState<string | null>(defaultHostId)
  const [choice, setChoice] = useState<SotChoice | null>(null)
  const [newName, setNewName] = useState<{ value: string; touched: boolean }>(() => ({ value: offeredProfileName(), touched: false }))
  /** Profiles THIS visit created — for the WORDING of "pull is not offered" only; whether it is offered is `seen.empty`. */
  const [created, setCreated] = useState<readonly string[]>([])
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<Exclude<CreateResult, { ok: true } | { outcome: 'maybe' }> | null>(null)
  /** The chosen profile as the user SAW it when confirming step 2 (or as `prepareRun` found it since). */
  const [seen, setSeen] = useState<SotNow | null>(null)
  /** host id → the ids it listed before this visit's first POST to it (null: never read). Set once per host. */
  const baselines = useRef(new Map<string, readonly string[] | null>())
  /** The create for this host + name ended unknown: the next press looks before it sends. */
  /** A profile that appeared after a create of unknown outcome: marked in the list as possibly the user's own. */
  const [maybeId, setMaybeId] = useState<string | null>(null)
  const [lookFirst, setLookFirst] = useState<{ hostId: string; name: string } | null>(null)
  const [localId, setLocalId] = useState<string>(MASTER_PROFILE_ID)
  const [direction, setDirection] = useState<SyncDirection | null>(null)
  const [saveFirst, setSaveFirst] = useState(true)
  const [saveName, setSaveName] = useState<{ value: string; touched: boolean }>(() => ({ value: offeredProfileName(), touched: false }))
  const [run, setRun] = useState<RunState | null>(null)
  const alive = useRef(true)

  const sot = useSotProfiles(refusal === null && step !== 'stop' ? hostId : null)
  // Subscribed so that a change re-renders — and with it the premise check below.
  const masterNow = useProfileStore((s) => s.masterHostId !== null && s.masterProfileId !== null && s.masterEndpoint !== null)
  const hosts = useHostStore((s) => s.hosts)
  const runtime = useHostStore((s) => s.runtime)
  const slaves = useLocalProfilesStore((s) => s.slaves)

  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  // Electron's hostname is resolved on demand; asked for by opening the WIZARD (a click), never by the page.
  useEffect(() => {
    if (refusal !== null) return
    void ensureDefaultDeviceName().then(() => {
      if (!alive.current) return
      setNewName((n) => (n.touched ? n : { value: offeredProfileName(), touched: false }))
      setSaveName((n) => (n.touched ? n : { value: offeredProfileName(), touched: false }))
    })
  }, [refusal])

  const busy = stopping || creating || run?.phase === 'running' || run?.phase === 'checking'
  const runStarted = run !== null && run.phase !== 'idle'

  /** Back to the step that still stands, and what no longer holds is forgotten. True = something was broken. */
  const sendBack = (broken: { step: StepId; reason: PremiseReason }): void => {
    setStep(broken.step)
    setNotice(broken.reason)
    setRun(null)
    if (broken.step === 'stop') setHadMaster(true)
    if (broken.reason === 'host-gone' || broken.reason === 'host-offline') {
      setHostId(defaultHostId())
      setChoice(null)
      setSeen(null)
    }
    if (broken.reason === 'local-gone') setLocalId(MASTER_PROFILE_ID)
  }

  const recheck = (at: StepId): boolean => {
    const broken = brokenPremise(at, hostId, localId)
    if (broken !== null) sendBack(broken)
    return broken !== null
  }

  useEffect(() => {
    // `stopResult`: the sync HAS stopped, the host was not told — that is said, and the user is the one who goes on.
    if (refusal !== null || busy || runStarted || stopResult !== null) return
    recheck(step)
    // `recheck` reads the stores itself; the values below are what makes this run again when they move.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [masterNow, hosts, runtime, slaves, step, busy, runStarted, refusal, stopResult])

  const go = (to: StepId): void => {
    if (recheck(to)) return
    setNotice(null)
    setStep(to)
  }

  const stop = async (): Promise<void> => {
    if (stopping) return
    setStopping(true)
    let result: DetachResult
    try {
      result = await detachMaster()
    } catch {
      result = { ok: false, reason: 'daemon-not-told', detail: '' } // start.ts does not throw; the device has stopped either way
    }
    if (!alive.current) return
    setStopping(false)
    if (result.ok) go('sot')
    else setStopResult(result)
  }

  const rows = sot.view?.kind === 'rows' ? sot.view.rows : null
  const chosenRow = choice?.kind === 'existing' ? (rows?.find((r) => r.id === choice.id) ?? null) : null
  const sotReady = hostId !== null && (choice?.kind === 'new' ? normalizeLocalProfileName(newName.value) !== null : chosenRow !== null)

  const confirmSot = async (): Promise<void> => {
    if (!sotReady || hostId === null || choice === null || creating) return
    if (choice.kind === 'existing') {
      if (chosenRow === null) return
      setSeen(sotNow(chosenRow)) // what the user is looking at, this instant
      return go('local')
    }
    const name = normalizeLocalProfileName(newName.value)
    if (name === null) return
    if (!baselines.current.has(hostId)) baselines.current.set(hostId, rows === null ? null : rows.map((row) => row.id))
    setCreating(true)
    setCreateError(null)
    const r = await createSotProfile(hostId, name, baselines.current.get(hostId) ?? null, lookFirst !== null && lookFirst.hostId === hostId && lookFirst.name === name)
    if (!alive.current) return
    setCreating(false)
    if (!r.ok) {
      // A definite refusal created nothing. Anything else: the next press for this host and name LOOKS FIRST —
      // `not-created` too (a POST still on its way may land after the look).
      setLookFirst(r.outcome === 'failed' ? null : { hostId, name })
      if (r.outcome !== 'failed') sot.reload() // the list is where the user can see what the host holds now
      if (r.outcome === 'maybe') {
        // Pointed at, never taken: it may be another device's. The user chooses it from the list, or renames.
        setMaybeId(r.candidateId)
        setNotice('create-maybe')
        return
      }
      return setCreateError(r)
    }
    setLookFirst(null)
    setCreated((c) => [...c, r.id])
    setChoice({ kind: 'existing', id: r.id, name })
    setSeen(sotNow({ sections: [] })) // created — or found with no section: empty, until `prepareRun` hears otherwise
    sot.reload()
    go('local')
  }

  // === what the later steps read of the earlier ones ===
  const profileId = choice?.kind === 'existing' ? choice.id : null
  const profileName = choice?.kind === 'existing' ? (chosenRow?.name ?? choice.name ?? choice.id) : ''
  // Whether pull is offered is what the profile HELD when last looked at; `created` only picks the wording.
  const pullUnavailable: 'new' | 'empty' | null = seen === null || !seen.empty ? null : profileId !== null && created.includes(profileId) ? 'new' : 'empty'
  const effectiveDirection: SyncDirection | null = pullUnavailable !== null ? 'push' : direction
  const worldReason = useMasterWorldReason()
  const saveNameOk = normalizeLocalProfileName(saveName.value) !== null
  const directionReady = effectiveDirection === 'push' || (effectiveDirection === 'pull' && (!saveFirst || saveNameOk))

  const confirmDirection = (): void => {
    if (!directionReady || hostId === null || profileId === null || effectiveDirection === null) return
    const plan: WizardPlan = { hostId, profileId, localId, direction: effectiveDirection, saveAs: effectiveDirection === 'pull' && saveFirst ? saveName.value : null }
    if (recheck('run')) return
    setRun({ plan, states: subStepsOf(plan).map(() => 'pending'), phase: 'idle', failure: null, checkFailed: null, localName: localId === MASTER_PROFILE_ID ? null : (slaves[localId]?.name ?? null) })
    setNotice(null)
    setStep('run')
  }

  const execute = async (from: number): Promise<void> => {
    if (run === null || run.phase === 'running' || run.phase === 'checking' || run.phase === 'done') return
    const before = run.phase
    const promoted = run.states[subStepsOf(run.plan).indexOf('promote')] === 'done'
    const labels = { profile: profileName, host: hostId === null ? '' : (hosts[hostId]?.name ?? hostId) }
    const draft: WizardDraft = { hostId: run.plan.hostId, profileId: run.plan.profileId, seen: seen?.fingerprint ?? null, localId: run.plan.localId, direction: run.plan.direction, saveAs: run.plan.saveAs }
    setRun((r) => (r === null ? r : { ...r, phase: 'checking', checkFailed: null }))
    // THE door: nothing irreversible happens before it has answered with a plan.
    const prepared = await prepareRun(draft, promoted)
    if (!alive.current) return
    if (!prepared.ok) {
      if (prepared.reason === 'list-failed') return setRun((r) => (r === null ? r : { ...r, phase: before, checkFailed: prepared.request }))
      if (prepared.reason === 'profile-changed') {
        setSeen(prepared.now)
        setDirection(null)
        // After a promote the chosen profile IS the master: the next plan must not promote again.
        if (promoted) setLocalId(MASTER_PROFILE_ID)
        setRun(null)
        setNotice(prepared.now.empty ? 'profile-emptied' : 'profile-changed')
        sot.reload()
        return setStep('direction')
      }
      if (prepared.reason === 'profile-gone') {
        setChoice(null)
        setSeen(null)
        setDirection(null)
        if (promoted) setLocalId(MASTER_PROFILE_ID)
        setRun(null)
        setNotice('profile-gone')
        sot.reload()
        return setStep('sot')
      }
      if (prepared.reason === 'incomplete') return setRun((r) => (r === null ? r : { ...r, phase: before }))
      return sendBack({ step: PREMISE_STEP[prepared.reason], reason: prepared.reason })
    }
    // A first run takes the door's plan; a retry keeps its own (its sub-steps' states are indexed by it).
    const plan = from === 0 && before === 'idle' ? prepared.plan : run.plan
    setRun((r) => (r === null ? r : { ...r, plan, phase: 'running', failure: null }))
    const result = await runPlan(plan, from, (index, state) => {
      if (alive.current) setRun((r) => (r === null ? r : { ...r, states: r.states.map((s, i) => (i === index ? state : s)) }))
    })
    // Said whether or not this component is still there — a pull replaces the world it lives in.
    announceRun(result, plan, labels, alive.current)
    if (!alive.current) return
    setRun((r) => (r === null ? r : result.done ? { ...r, phase: 'done' } : { ...r, phase: 'failed', failure: { at: result.failedAt, reason: result.reason } }))
  }

  const restart = (): void => {
    setRun(null)
    setNotice(null)
    setStopResult(null)
    setChoice(null)
    setSeen(null)
    setDirection(null)
    setLocalId(MASTER_PROFILE_ID)
    const attached = masterAttached()
    setHadMaster(attached)
    setStep(attached ? 'stop' : 'sot')
  }

  const shown = ORDER.filter((id) => id !== 'stop' || hadMaster)

  return (
    <div data-testid="profile-wizard" data-step={refusal !== null ? 'refused' : step} role="group" aria-label={t('settings.profile.wizard.title')} className="mt-2 rounded-md border border-border-default p-3">
      <div className="flex items-start justify-between gap-2">
        <h4 className="text-sm text-text-primary">{t('settings.profile.wizard.title')}</h4>
        <button type="button" data-testid="profile-wizard-close" disabled={busy} onClick={onClose} className={BTN}>
          {t(run?.phase === 'done' ? 'common.close' : 'common.cancel')}
        </button>
      </div>

      {refusal !== null ? (
        <p data-testid="profile-wizard-refused" data-reason={refusal} className={NOTICE}>
          {t(`settings.profile.wizard.refused.${refusal.replace(/-/g, '_')}`)}
        </p>
      ) : (
        <>
          <ol data-testid="profile-wizard-steps" className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs">
            {shown.map((id, index) => {
              const state = id === step ? 'current' : ORDER.indexOf(id) < ORDER.indexOf(step) ? 'done' : 'todo'
              return (
                <li key={id} data-testid={`profile-wizard-step-${id}`} data-state={state} aria-current={state === 'current' ? 'step' : undefined} className={state === 'current' ? 'text-text-primary' : 'text-text-muted'}>
                  {index + 1}. {t(`settings.profile.wizard.step.${id}`)}
                </li>
              )
            })}
          </ol>

          {notice !== null && (
            <p data-testid="profile-wizard-notice" data-reason={notice} role="status" className={NOTICE}>
              {t(`settings.profile.wizard.notice.${notice.replace(/-/g, '_')}`)}
            </p>
          )}

          {step === 'stop' && (
            <div className="mt-3 text-xs">
              <p className="text-text-secondary">{t('settings.profile.wizard.stop.what')}</p>
              <p className="text-text-secondary">{t('settings.profile.wizard.stop.safe')}</p>
              {stopResult !== null && (
                <p data-testid="profile-wizard-stop-not-told" data-reason={stopResult.reason} role="status" className={NOTICE}>
                  {t('settings.profile.wizard.stop.not_told')}
                </p>
              )}
              <div className="mt-3 flex flex-wrap gap-2">
                {stopResult === null ? (
                  <button type="button" data-testid="profile-wizard-stop-confirm" aria-busy={stopping} disabled={stopping} onClick={() => void stop()} className={BTN}>
                    {stopping && <ArrowsClockwise size={14} className="animate-spin" />}
                    {t('settings.profile.wizard.stop.confirm')}
                  </button>
                ) : (
                  <button type="button" data-testid="profile-wizard-stop-continue" onClick={() => { setStopResult(null); go('sot') }} className={BTN}>
                    {t('settings.profile.wizard.stop.continue')}
                  </button>
                )}
              </div>
            </div>
          )}

          {step === 'sot' && (
            <SotStep
              hostId={hostId}
              onHost={(id) => {
                setHostId(id)
                setChoice(null)
                setSeen(null)
                setCreateError(null)
              }}
              view={sot.view}
              reload={sot.reload}
              choice={choice}
              onChoice={(c) => {
                setChoice(c)
                setCreateError(null)
              }}
              newName={newName.value}
              onNewName={(value) => setNewName({ value, touched: true })}
              createError={createError}
              maybeId={maybeId}
              disabled={creating}
            />
          )}

          {step === 'local' && <LocalStep localId={localId} onLocal={setLocalId} worldReason={worldReason} />}

          {step === 'direction' && (
            <DirectionStep
              profileName={profileName}
              pullUnavailable={pullUnavailable}
              direction={effectiveDirection}
              onDirection={setDirection}
              localId={localId}
              saveFirst={saveFirst}
              onSaveFirst={setSaveFirst}
              saveName={saveName.value}
              onSaveName={(value) => setSaveName({ value, touched: true })}
              saveNameOk={saveNameOk}
            />
          )}

          {step === 'run' && run !== null && <RunStep run={run} profileName={profileName} hostName={hostId === null ? '' : (hosts[hostId]?.name ?? hostId)} />}

          {step !== 'stop' && (
            <div className="mt-3 flex flex-wrap items-center gap-2">
              {(step === 'local' || step === 'direction' || (step === 'run' && run?.phase === 'idle')) && (
                <button type="button" data-testid="profile-wizard-back" onClick={() => go(ORDER[ORDER.indexOf(step) - 1])} className={BTN}>
                  {t('settings.profile.wizard.back')}
                </button>
              )}
              {step === 'sot' && (
                <button type="button" data-testid="profile-wizard-next" aria-busy={creating} disabled={!sotReady || creating} onClick={() => void confirmSot()} className={BTN}>
                  {creating && <ArrowsClockwise size={14} className="animate-spin" />}
                  {t(choice?.kind === 'new' ? 'settings.profile.wizard.sot.create_next' : 'settings.profile.wizard.next')}
                </button>
              )}
              {step === 'local' && (
                <button type="button" data-testid="profile-wizard-next" disabled={worldReason !== null} onClick={() => go('direction')} className={BTN}>
                  {t('settings.profile.wizard.next')}
                </button>
              )}
              {step === 'direction' && (
                <button type="button" data-testid="profile-wizard-next" disabled={!directionReady} onClick={confirmDirection} className={BTN}>
                  {t('settings.profile.wizard.next')}
                </button>
              )}
              {step === 'run' && run?.phase === 'idle' && (
                <button type="button" data-testid="profile-wizard-start" onClick={() => void execute(0)} className={BTN}>
                  {t('settings.profile.wizard.run.start')}
                </button>
              )}
              {step === 'run' && run?.phase === 'failed' && run.failure !== null && (
                FINAL[subStepsOf(run.plan)[run.failure.at]].has(run.failure.reason) ? (
                  <button type="button" data-testid="profile-wizard-restart" onClick={restart} className={BTN}>
                    {t('settings.profile.wizard.run.restart')}
                  </button>
                ) : (
                  <button type="button" data-testid="profile-wizard-retry" onClick={() => void execute(run.failure!.at)} className={BTN}>
                    <ArrowsClockwise size={14} />
                    {t('settings.profile.wizard.run.retry')}
                  </button>
                )
              )}
            </div>
          )}
        </>
      )}
    </div>
  )
}

const SUBSTEP_ICON: Record<SubStepState, typeof Circle> = { pending: Circle, running: ArrowsClockwise, done: CheckCircle, failed: WarningCircle }
const SUBSTEP_TONE: Record<SubStepState, string> = { pending: 'text-text-muted', running: 'text-text-secondary', done: 'text-green-500', failed: 'text-red-500' }

function RunStep({ run, profileName, hostName }: { run: RunState; profileName: string; hostName: string }) {
  const t = useI18nStore((s) => s.t)
  const steps = subStepsOf(run.plan)
  const label = (id: SubStepId): string => {
    if (id === 'promote') return t('settings.profile.wizard.run.promote', { name: run.localName ?? '' })
    if (id === 'save') return t('settings.profile.wizard.run.save', { name: run.plan.saveAs ?? '' })
    return t(`settings.profile.wizard.run.attach_${run.plan.direction}`, { profile: profileName, host: hostName })
  }
  const done = (id: SubStepId): boolean => run.states[steps.indexOf(id)] === 'done'
  const failed = run.failure === null ? null : steps[run.failure.at]

  return (
    <div className="mt-3 text-xs">
      <p className="text-text-secondary">{t(run.phase === 'idle' ? 'settings.profile.wizard.run.summary' : 'settings.profile.wizard.run.progress')}</p>
      {run.phase === 'checking' && (
        <p data-testid="profile-wizard-checking" role="status" className="flex items-center gap-1.5 text-text-secondary">
          <ArrowsClockwise size={14} className="animate-spin" />
          {t('settings.profile.wizard.run.checking')}
        </p>
      )}
      {run.checkFailed !== null && run.phase !== 'checking' && (
        <p data-testid="profile-wizard-check-failed" data-reason={run.checkFailed} role="alert" className="text-red-500">
          {t('settings.profile.wizard.run.check_failed')} {t(requestKey(run.checkFailed))}
        </p>
      )}
      <ul data-testid="profile-wizard-summary" className="mt-1 flex flex-col gap-1">
        {steps.map((id, i) => {
          const state = run.states[i]
          const Icon = SUBSTEP_ICON[state]
          return (
            <li key={id} data-testid={`profile-wizard-substep-${id}`} data-state={state} className="flex items-start gap-1.5 text-text-primary">
              <Icon size={14} className={`mt-0.5 shrink-0 ${SUBSTEP_TONE[state]} ${state === 'running' ? 'animate-spin' : ''}`} />
              <span>
                {label(id)} <span className={SUBSTEP_TONE[state]}>— {t(`settings.profile.wizard.run.state.${state}`)}</span>
              </span>
            </li>
          )
        })}
      </ul>

      {run.phase === 'failed' && run.failure !== null && failed !== null && (
        <div data-testid="profile-wizard-failure" data-step={failed} data-reason={run.failure.reason} role="alert" className="mt-2 flex flex-col gap-0.5">
          <p className="text-red-500">{t(reasonKey(failed, run.failure.reason))}</p>
          <p className="text-text-secondary">{t('settings.profile.wizard.now.stopped_here')}</p>
          {done('promote') && <p className="text-text-secondary">{t('settings.profile.wizard.now.promoted', { name: run.localName ?? '' })}</p>}
          {done('save') && <p className="text-text-secondary">{t('settings.profile.wizard.now.saved', { name: run.plan.saveAs ?? '' })}</p>}
          {!done('promote') && !done('save') && <p className="text-text-secondary">{t('settings.profile.wizard.now.nothing_changed')}</p>}
          <p className="text-text-secondary">{t('settings.profile.wizard.now.not_syncing')}</p>
        </div>
      )}

      {run.phase === 'done' && (
        <p data-testid="profile-wizard-done" role="status" className="mt-2 text-text-primary">
          {t(`settings.profile.wizard.run.done_${run.plan.direction}`, { profile: profileName, host: hostName })}
        </p>
      )}
    </div>
  )
}
