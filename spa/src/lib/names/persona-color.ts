// spa/src/lib/names/persona-color.ts — the colour the initials sit on when a persona has no stored image. Where a host
// is known its colour is used instead (`<Avatar fallbackColor>`); on the persona page there is no host, so the colour is
// derived from the code: the same persona always gets the same one.
export function personaColor(code: string): string {
  let h = 0
  for (const ch of code) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return `hsl(${h % 360} 40% 38%)`
}
