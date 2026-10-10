// spa/src/components/room/RoomProse.tsx — the agent's prose in the room
// (spec §4.1). What MessageBubble's assistant arm was, at the pane's one left
// edge: no bubble, no percentage clamp, only a reading measure so a paragraph
// does not run the width of a wide pane. Code blocks inside it still get the
// measure; output, diffs and tables are other blocks and take the full width.
import { useMemo, type ComponentProps, type CSSProperties } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeHighlight from 'rehype-highlight'
import 'highlight.js/styles/github-dark.css'
import StreamCursor from '../StreamCursor'
import { useWorkerSettingsStore } from '../../stores/useWorkerSettingsStore'
import { getWorkerTheme, workerThemeStyle } from '../../lib/worker-theme/registry'
import { cachedMarkdown } from '../../lib/conversations/markdown-cache'

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
  /**
   * Keep the rendered markdown under this key (the deck passes the item id, #2469): a remount of the same finished text reuses
   * the React tree instead of parsing and highlighting again. Ignored while `streaming`, which is never kept.
   */
  cacheKey?: string
}

const REMARK_PLUGINS = [remarkGfm]
const REHYPE_PLUGINS = [rehypeHighlight]

/** react-markdown's own function component, called directly: its result is a plain React node that can be kept. */
function renderMarkdown(content: string) {
  return ReactMarkdown({ children: content, remarkPlugins: REMARK_PLUGINS, rehypePlugins: REHYPE_PLUGINS, components: COMPONENTS })
}

/**
 * The same box as RoomProse with the markdown source drawn as plain text (`white-space: pre-wrap`, the prose's own font and
 * line height), for a message far off screen (#2469). The whole text is there, so the browser's find still reaches it; it is
 * deliberately NOT a search unit — the transcript search indexes `proseText(content)`, which this is not, so its ordinals would
 * not line up. A reader that searches must draw the full RoomProse for a hit first.
 */
export function RoomProseLight({ content }: { content: string }) {
  const themeId = useWorkerSettingsStore((s) => s.theme)
  const themeVars = useMemo(() => workerThemeStyle(getWorkerTheme(themeId)) as CSSProperties, [themeId])
  return (
    <div data-testid="room-prose-light" className="max-w-[90ch] text-text-primary" style={themeVars}>
      <div className="prose prose-invert worker-prose max-w-none">
        <div className="whitespace-pre-wrap break-words">{content}</div>
      </div>
    </div>
  )
}

export default function RoomProse({ content, streaming, searchUnit, cacheKey }: Props) {
  // `.worker-prose` (index.css) reads the `--wt-*` vars; the execution pane root is not the only place this renders (deck,
  // chat, peer blocks), so it carries the selected theme's vars itself (#2463). Same theme source as the pane root.
  const themeId = useWorkerSettingsStore((s) => s.theme)
  const themeVars = useMemo(() => workerThemeStyle(getWorkerTheme(themeId)) as CSSProperties, [themeId])
  return (
    <div data-testid="room-prose" className="max-w-[90ch] text-text-primary" style={themeVars}>
      <div data-search-unit={searchUnit} className="prose prose-invert worker-prose max-w-none">
        {cacheKey !== undefined && !streaming
          ? cachedMarkdown(cacheKey, content, () => renderMarkdown(content))
          : (
            <ReactMarkdown remarkPlugins={REMARK_PLUGINS} rehypePlugins={REHYPE_PLUGINS} components={COMPONENTS}>
              {content}
            </ReactMarkdown>
          )}
      </div>
      {streaming && <StreamCursor />}
    </div>
  )
}
