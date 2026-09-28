// spa/src/components/room/RoomProse.tsx — the agent's prose in the room
// (spec §4.1). What MessageBubble's assistant arm was, at the pane's one left
// edge: no bubble, no percentage clamp, only a reading measure so a paragraph
// does not run the width of a wide pane. Code blocks inside it still get the
// measure; output, diffs and tables are other blocks and take the full width.
import type { ComponentProps } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeHighlight from 'rehype-highlight'
import 'highlight.js/styles/github-dark.css'
import StreamCursor from '../StreamCursor'

// A table (GFM, spec §5.3) scrolls horizontally inside its own wrapper —
// never the whole transcript. The wrapper adds no text, so markdown-text.ts's
// parity holds.
const COMPONENTS = {
  table: ({ node: _node, ...props }: ComponentProps<'table'> & { node?: unknown }) => (
    <div className="overflow-x-auto">
      <table {...props} />
    </div>
  ),
}

interface Props {
  content: string
  /** Append a blinking cursor after the markdown body (the P-B2 typewriter). */
  streaming?: boolean
  /**
   * The search anchor, on the markdown body only (not the cursor). The index
   * holds `proseText(content)` — this body's `textContent` — so any change to
   * the plugins here must be mirrored in lib/nex/markdown-text.ts (its tests
   * render this component and compare).
   */
  searchUnit?: string
}

export default function RoomProse({ content, streaming, searchUnit }: Props) {
  return (
    <div data-testid="room-prose" className="max-w-[90ch] text-text-primary">
      <div data-search-unit={searchUnit} className="prose prose-invert worker-prose max-w-none">
        <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]} components={COMPONENTS}>
          {content}
        </ReactMarkdown>
      </div>
      {streaming && <StreamCursor />}
    </div>
  )
}
