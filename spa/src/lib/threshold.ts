// spa/src/lib/threshold.ts — the percentage threshold's step and bounds (compaction control), shared by ThresholdStepper and
// the callers that apply its ±step deltas. Kept out of the component file so it exports components only (react-refresh).
export const THRESHOLD_STEP = 5
export const THRESHOLD_MIN = 1
export const THRESHOLD_MAX = 100

export function clampThreshold(v: number): number {
  return Math.min(THRESHOLD_MAX, Math.max(THRESHOLD_MIN, v))
}
