export function nextWorkspaceName(existingNames: string[]): string {
  const nameSet = new Set(existingNames)
  for (let n = 1; ; n++) {
    const candidate = `Workspace ${n}`
    if (!nameSet.has(candidate)) return candidate
  }
}
