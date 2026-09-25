// spa/src/components/room/RoomProse.tsx — the agent's prose in the room
// (spec §4.1). What MessageBubble's assistant arm was, at the pane's one left
// edge: no bubble, no percentage clamp, only a reading measure so a paragraph
// does not run the width of a wide pane. Code blocks inside it still get the
// measure; output, diffs and tables are other blocks and take the full width.
import ReactMarkdown from 'react-markdown'
import rehypeHighlight from 'rehype-highlight'
import 'highlight.js/styles/github-dark.css'
import StreamCursor from '../StreamCursor'

interface Props {
  content: string
  /** Append a blinking cursor after the markdown body (the P-B2 typewriter). */
  streaming?: boolean
}

export default function RoomProse({ content, streaming }: Props) {
  return (
    <div data-testid="room-prose" className="max-w-[90ch] text-sm leading-[1.7] text-text-primary">
      <div className="prose prose-invert prose-sm max-w-none">
        <ReactMarkdown rehypePlugins={[rehypeHighlight]}>
          {content}
        </ReactMarkdown>
      </div>
      {streaming && <StreamCursor />}
    </div>
  )
}
