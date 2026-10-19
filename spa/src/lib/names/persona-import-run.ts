// spa/src/lib/names/persona-import-run.ts — the step between a dry-run import plan and the save: the avatars the plan
// marked for redrawing are drawn (name pool v2 spec §4.1). `draw` is passed in, not imported: only the persona page's own
// folder may import `persona-draw` at runtime (the DiceBear guard), and this keeps the logic testable without a canvas.
import type { ImportPlan } from './persona-import'
import type { Persona, PersonaAvatarRecipe } from './persona-types'

export interface RedrawOutcome {
  /** `plan.result` with the redrawn images in. */
  result: Persona[]
  /** The codes whose avatar could not be drawn; each keeps the recipe and the image it had before. */
  failed: string[]
}

const NO_AVATAR: PersonaAvatarRecipe = { source: '' }

/**
 * Draws, one at a time, the recipe of every code in `plan.redraw`. A recipe without a source means "initials": no image,
 * nothing to draw. When a draw fails the persona goes back to the recipe AND the image it had (or to initials, for a new
 * one): a recipe that names an image that was never drawn would make the next redraw look unnecessary.
 */
export async function redrawForImport(
  plan: ImportPlan,
  existing: readonly Persona[],
  draw: (recipe: PersonaAvatarRecipe) => Promise<string>,
): Promise<RedrawOutcome> {
  const before = new Map(existing.map((p) => [p.code, p]))
  const result = plan.result.map((p) => ({ ...p }))
  const failed: string[] = []
  for (const code of plan.redraw) {
    const p = result.find((x) => x.code === code)
    if (!p) continue
    if (p.avatar.source === '') { p.avatar_data = ''; continue }
    try {
      p.avatar_data = await draw(p.avatar)
    } catch {
      const was = before.get(code)
      p.avatar = was?.avatar ?? NO_AVATAR
      p.avatar_data = was?.avatar_data ?? ''
      failed.push(code)
    }
  }
  return { result, failed }
}
