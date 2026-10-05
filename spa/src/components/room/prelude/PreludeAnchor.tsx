// spa/src/components/room/prelude/PreludeAnchor.tsx — keeps the reader's
// place when the prelude grows above them (spec §5.4). React's documented
// prepend pattern: getSnapshotBeforeUpdate reads the section's height right
// before the commit that changes it; componentDidUpdate hands the growth to
// the transcript's scroll. Measuring this wrapper, not the whole box, keeps
// a live message landing in the same commit out of the correction.
import { Component, createRef, type ReactNode } from 'react'

interface PreludeAnchorProps {
  /** Changes whenever the section may change height: pages applied + status (a loading row comes and goes). */
  version: string
  onGrow: (delta: number) => void
  children: ReactNode
}

export default class PreludeAnchor extends Component<PreludeAnchorProps> {
  private el = createRef<HTMLDivElement>()

  getSnapshotBeforeUpdate(prev: PreludeAnchorProps): number | null {
    return prev.version !== this.props.version ? (this.el.current?.offsetHeight ?? 0) : null
  }

  componentDidUpdate(_prev: PreludeAnchorProps, _state: unknown, before: number | null) {
    if (before === null) return
    const grown = (this.el.current?.offsetHeight ?? 0) - before
    if (grown !== 0) this.props.onGrow(grown)
  }

  render() {
    return <div ref={this.el} data-testid="prelude-anchor">{this.props.children}</div>
  }
}
