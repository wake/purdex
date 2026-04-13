import type { PathData } from './icon-path-cache'

/** Render SVG <path> elements from PathData (shared by WorkspaceIcon & WorkspaceIconPicker) */
export function renderPaths(data: PathData) {
  if (typeof data === 'string') return <path d={data} />
  return data.map((p, i) =>
    typeof p === 'string'
      ? <path key={i} d={p} />
      : <path key={i} d={p.d} opacity={p.o} />,
  )
}
