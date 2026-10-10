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
import { rehypeCjk } from '../../lib/conversations/cjk'
import { CjkText } from '../deck/CjkText'

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
  /** The deck and the chat space Chinese text 1px apart: CJK runs are wrapped in `span.cjk` (lib/conversations/cjk.ts). The room and the execution pane do not. */
  cjk?: boolean
}

const REMARK_PLUGINS = [remarkGfm]
const REHYPE_PLUGINS = [rehypeHighlight]
// after the highlighter, which has already turned code into its own spans (the CJK step skips code anyway)
const REHYPE_PLUGINS_CJK = [rehypeHighlight, rehypeCjk]

/** react-markdown's own function component, called directly: its result is a plain React node that can be kept. */
function renderMarkdown(content: string, cjk?: boolean) {
  return ReactMarkdown({ children: content, remarkPlugins: REMARK_PLUGINS, rehypePlugins: cjk ? REHYPE_PLUGINS_CJK : REHYPE_PLUGINS, components: COMPONENTS })
}

/**
 * The markdown body. The streaming text and the finished (kept) text both come out of this one component and are both
 * react-markdown's own node tree, so when a message stops streaming React reconciles the same elements and keeps their DOM
 * (a text selection, a code block scrolled sideways) instead of mounting a different element type in their place.
 */
function ProseBody({ content, keep, cjk }: { content: string; keep?: string; cjk?: boolean }) {
  // the kept tree carries the CJK spans: a keyed entry is one rendering, so the key says which
  return keep !== undefined ? cachedMarkdown(cjk ? `${keep}#cjk` : keep, content, () => renderMarkdown(content, cjk)) : renderMarkdown(content, cjk)
}

/**
 * The same box as RoomProse with the markdown source drawn as plain text (`white-space: pre-wrap`, the prose's own font and
 * line height), for a message far off screen (#2469). The whole text is there, so the browser's find still reaches it; it is
 * deliberately NOT a search unit — the transcript search indexes `proseText(content)`, which this is not, so its ordinals would
 * not line up. A reader that searches must draw the full RoomProse for a hit first.
 */
export function RoomProseLight({ content, cjk }: { content: string; cjk?: boolean }) {
  const themeId = useWorkerSettingsStore((s) => s.theme)
  const themeVars = useMemo(() => workerThemeStyle(getWorkerTheme(themeId)) as CSSProperties, [themeId])
  return (
    <div data-testid="room-prose-light" className="max-w-[90ch] text-text-primary" style={themeVars}>
      <div className="prose prose-invert worker-prose max-w-none">
        <div className="whitespace-pre-wrap break-words">{cjk ? <CjkText text={content} /> : content}</div>
      </div>
    </div>
  )
}

export default function RoomProse({ content, streaming, searchUnit, cacheKey, cjk }: Props) {
  // `.worker-prose` (index.css) reads the `--wt-*` vars; the execution pane root is not the only place this renders (deck,
  // chat, peer blocks), so it carries the selected theme's vars itself (#2463). Same theme source as the pane root.
  const themeId = useWorkerSettingsStore((s) => s.theme)
  const themeVars = useMemo(() => workerThemeStyle(getWorkerTheme(themeId)) as CSSProperties, [themeId])
  return (
    <div data-testid="room-prose" className="max-w-[90ch] text-text-primary" style={themeVars}>
      <div data-search-unit={searchUnit} className="prose prose-invert worker-prose max-w-none">
        <ProseBody content={content} keep={cacheKey !== undefined && !streaming ? cacheKey : undefined} cjk={cjk} />
      </div>
      {streaming && <StreamCursor />}
    </div>
  )
}
