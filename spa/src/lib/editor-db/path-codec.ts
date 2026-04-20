export function canonicalizePath(input: string): string {
  if (!input.startsWith('/')) {
    throw new Error('Path must be absolute')
  }

  if (input === '/') {
    return '/'
  }

  const parts = input.split('/').filter(Boolean)
  if (parts.some((part) => part === '.' || part === '..')) {
    throw new Error('Dot segments are not allowed')
  }

  return `/${parts.join('/')}`
}

export function splitParentPath(path: string): { parentPath: string; name: string } {
  const canonical = canonicalizePath(path)
  if (canonical === '/') {
    throw new Error('Root has no parent')
  }

  const lastSlashIndex = canonical.lastIndexOf('/')
  const parentPath = lastSlashIndex === 0 ? '/' : canonical.slice(0, lastSlashIndex)
  const name = canonical.slice(lastSlashIndex + 1)

  return { parentPath, name }
}

export function isDescendantPath(candidate: string, base: string): boolean {
  const canonicalCandidate = canonicalizePath(candidate)
  const canonicalBase = canonicalizePath(base)

  if (canonicalBase === '/') {
    return canonicalCandidate !== '/'
  }

  return canonicalCandidate === canonicalBase || canonicalCandidate.startsWith(`${canonicalBase}/`)
}
