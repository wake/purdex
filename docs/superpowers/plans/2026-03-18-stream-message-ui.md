# Stream Message UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render all CC stream message types correctly in ConversationView, matching the approved demo design (`docs/research/stream-ui-demo.html`).

**Architecture:** Three layers of change: (1) Go ParseJSONL filters CC internal markup and preserves `isMeta` flag, (2) SPA message rendering in ConversationView handles all content block types, (3) new/updated React components for thinking, tool result, command bubble, interrupted, and assistant text without bubble. ToolCallBlock switches to unified `Wrench` icon.

**Tech Stack:** Go (history parser), React 19 / Tailwind CSS / Phosphor Icons / react-markdown / vitest

**Design Reference:** `docs/research/stream-ui-demo.html`

---

## File Structure

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `internal/history/history.go` | Filter `isMeta` + `<local-command-caveat>` + `<local-command-stdout>`, parse `<command-name>` into clean text |
| Modify | `internal/history/history_test.go` | Test CC internal markup filtering |
| Modify | `spa/src/components/MessageBubble.tsx` | User: keep bubble. Assistant: no bubble, direct markdown output |
| Modify | `spa/src/components/MessageBubble.test.tsx` | Test new assistant style + user bubble |
| Modify | `spa/src/components/ToolCallBlock.tsx` | Unified `Wrench` icon, remove per-tool icons |
| Modify | `spa/src/components/ToolCallBlock.test.tsx` | Update icon assertion |
| Create | `spa/src/components/ThinkingBlock.tsx` | Collapsible thinking block (`ph-brain`) |
| Create | `spa/src/components/ThinkingBlock.test.tsx` | Test collapse/expand, content render |
| Create | `spa/src/components/ToolResultBlock.tsx` | Collapsible tool result (success/error) |
| Create | `spa/src/components/ToolResultBlock.test.tsx` | Test success/error states, collapse |
| Modify | `spa/src/components/ConversationView.tsx` | Wire all block types, filter user meta, render command/interrupted |
| Modify | `spa/src/components/ConversationView.test.tsx` | Integration tests for all message types |

---

### Task 1: Go — Filter CC internal markup in ParseJSONL

**Files:**
- Modify: `internal/history/history.go`
- Modify: `internal/history/history_test.go`

ParseJSONL currently passes all user/assistant messages through. We need to:
- Skip messages with `isMeta: true` (CC system bookkeeping like `<local-command-caveat>`, `<local-command-stdout>`)
- For user messages whose string content matches `<command-name>X</command-name>`, extract `X` and replace content with it
- Skip `<synthetic>` assistant messages (model = `<synthetic>`, content = "No response requested.")

- [ ] **Step 1: Write failing tests**

Add to `internal/history/history_test.go`:

```go
func TestParseJSONLFiltersMeta(t *testing.T) {
	input := `{"type":"user","isMeta":true,"message":{"role":"user","content":"<local-command-caveat>Caveat: ignore</local-command-caveat>"}}
{"type":"user","message":{"role":"user","content":"<command-name>/exit</command-name>\n            <command-message>exit</command-message>\n            <command-args></command-args>"}}
{"type":"user","message":{"role":"user","content":"<local-command-stdout>Catch you later!</local-command-stdout>"}}
{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"No response requested."}],"stop_reason":"stop_sequence"}}
{"type":"user","message":{"role":"user","content":"ping"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn"}}
`
	messages, err := ParseJSONL(strings.NewReader(input), 2*1024*1024)
	if err != nil {
		t.Fatal(err)
	}

	// Should get 3 messages: /exit (parsed command), ping, pong
	if len(messages) != 3 {
		t.Fatalf("want 3 messages, got %d", len(messages))
	}

	// First: /exit command (extracted from <command-name>)
	msg0 := messages[0]["message"].(map[string]interface{})
	content0 := msg0["content"].([]interface{})
	block0 := content0[0].(map[string]interface{})
	if block0["text"] != "/exit" {
		t.Errorf("want /exit, got %v", block0["text"])
	}

	// Second: ping
	msg1 := messages[1]["message"].(map[string]interface{})
	content1 := msg1["content"].([]interface{})
	block1 := content1[0].(map[string]interface{})
	if block1["text"] != "ping" {
		t.Errorf("want ping, got %v", block1["text"])
	}

	// Third: pong
	if messages[2]["type"] != "assistant" {
		t.Errorf("want assistant, got %v", messages[2]["type"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/history/ -run TestParseJSONLFiltersMeta -v`
Expected: FAIL (isMeta messages not filtered, command not parsed)

- [ ] **Step 3: Implement filtering in ParseJSONL**

In `internal/history/history.go`, add filtering logic inside the scan loop:

```go
import "regexp"

var commandNameRegex = regexp.MustCompile(`<command-name>(.+?)</command-name>`)

// Inside ParseJSONL scan loop, after type check:

// Skip isMeta messages (CC system bookkeeping)
if isMeta, ok := entry["isMeta"].(bool); ok && isMeta {
    continue
}

// Skip <local-command-stdout> and <local-command-caveat>
if typ == "user" {
    if contentStr, ok := msg["content"].(string); ok {
        if strings.HasPrefix(contentStr, "<local-command-stdout>") ||
            strings.HasPrefix(contentStr, "<local-command-caveat>") {
            continue
        }
        // Parse <command-name>/exit</command-name> → "/exit"
        if m := commandNameRegex.FindStringSubmatch(contentStr); len(m) >= 2 {
            msg["content"] = []interface{}{
                map[string]interface{}{"type": "text", "text": m[1]},
            }
        }
    }
}

// Skip synthetic assistant messages
if typ == "assistant" {
    if model, _ := msg["model"].(string); model == "<synthetic>" {
        continue
    }
}
```

- [ ] **Step 4: Run tests to verify all pass**

Run: `go test ./internal/history/ -v`
Expected: ALL PASS (including existing tests)

- [ ] **Step 5: Commit**

```bash
git add internal/history/history.go internal/history/history_test.go
git commit -m "feat: filter CC internal markup in ParseJSONL"
```

---

### Task 2: SPA — MessageBubble redesign (user bubble, assistant no-bubble)

**Files:**
- Modify: `spa/src/components/MessageBubble.tsx`
- Modify: `spa/src/components/MessageBubble.test.tsx`

Currently MessageBubble wraps both user and assistant in bubbles. Per demo design:
- User: blue bubble, right-aligned, `border-radius: 12px 12px 4px 12px`, `padding: 6px 12px 6px 10px`
- Assistant: no bubble, left-aligned, direct markdown output

- [ ] **Step 1: Write failing tests**

Replace `spa/src/components/MessageBubble.test.tsx`:

```tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import MessageBubble from './MessageBubble'

beforeEach(() => { cleanup() })

describe('MessageBubble', () => {
  it('renders user message in a bubble', () => {
    const { container } = render(<MessageBubble role="user" content="hello" />)
    const bubble = container.querySelector('[data-testid="user-bubble"]')
    expect(bubble).toBeInTheDocument()
    expect(bubble).toHaveTextContent('hello')
  })

  it('renders assistant message without bubble wrapper', () => {
    const { container } = render(<MessageBubble role="assistant" content="hi there" />)
    expect(container.querySelector('[data-testid="user-bubble"]')).toBeNull()
    const text = container.querySelector('[data-testid="assistant-text"]')
    expect(text).toBeInTheDocument()
  })

  it('renders assistant markdown with code blocks', () => {
    render(<MessageBubble role="assistant" content="use `npm install`" />)
    expect(screen.getByText('npm install')).toBeInTheDocument()
  })

  it('applies correct user bubble classes', () => {
    const { container } = render(<MessageBubble role="user" content="test" />)
    const bubble = container.querySelector('[data-testid="user-bubble"]')
    expect(bubble?.className).toContain('bg-[#334a5e]')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/MessageBubble.test.tsx`
Expected: FAIL (no data-testid attributes, wrong styles)

- [ ] **Step 3: Rewrite MessageBubble**

```tsx
// spa/src/components/MessageBubble.tsx
import ReactMarkdown from 'react-markdown'
import rehypeHighlight from 'rehype-highlight'
import 'highlight.js/styles/github-dark.css'

interface Props {
  role: 'user' | 'assistant'
  content: string
}

export default function MessageBubble({ role, content }: Props) {
  if (role === 'user') {
    return (
      <div className="flex justify-end">
        <div
          data-testid="user-bubble"
          className="max-w-[75%] bg-[#334a5e] text-[#dde8f5] text-sm rounded-[12px_12px_4px_12px] px-3 py-1.5 pl-2.5"
        >
          <p className="whitespace-pre-wrap break-words">{content}</p>
        </div>
      </div>
    )
  }

  return (
    <div data-testid="assistant-text" className="max-w-[90%] text-sm leading-[1.7] text-[#e0e0e0]">
      <div className="prose prose-invert prose-sm max-w-none">
        <ReactMarkdown rehypePlugins={[rehypeHighlight]}>
          {content}
        </ReactMarkdown>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/MessageBubble.test.tsx`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/MessageBubble.tsx spa/src/components/MessageBubble.test.tsx
git commit -m "feat: MessageBubble — user bubble, assistant no-bubble"
```

---

### Task 3: SPA — ToolCallBlock unified Wrench icon

**Files:**
- Modify: `spa/src/components/ToolCallBlock.tsx`
- Modify: `spa/src/components/ToolCallBlock.test.tsx`

Replace all per-tool icons with a single `Wrench` icon from Phosphor.

- [ ] **Step 1: Write failing test**

Add to `spa/src/components/ToolCallBlock.test.tsx`:

```tsx
it('uses unified wrench icon for all tools', () => {
  const { container } = render(<ToolCallBlock tool="Bash" input={{}} />)
  // Should have a wrench icon, not a terminal icon
  expect(container.querySelector('[data-testid="tool-icon-wrench"]')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/ToolCallBlock.test.tsx`
Expected: FAIL (no wrench testid)

- [ ] **Step 3: Replace icons in ToolCallBlock**

```tsx
// spa/src/components/ToolCallBlock.tsx
import { useState } from 'react'
import { CaretRight, CaretDown, Wrench } from '@phosphor-icons/react'

interface Props {
  tool: string
  input: Record<string, unknown>
}

function getSummary(tool: string, input: Record<string, unknown>): string {
  switch (tool) {
    case 'Bash': return (input.command as string) ?? ''
    case 'Read':
    case 'Write':
    case 'Edit': return (input.file_path as string) ?? ''
    case 'WebFetch': return (input.url as string) ?? ''
    case 'Grep':
    case 'Glob': return (input.pattern as string) ?? ''
    case 'Agent': return (input.description as string) ?? ''
    default: return JSON.stringify(input).slice(0, 80)
  }
}

export default function ToolCallBlock({ tool, input }: Props) {
  const [expanded, setExpanded] = useState(false)
  const summary = getSummary(tool, input)

  return (
    <div className="rounded-lg border border-[#2a2a2a] bg-[#1e1e1e] text-sm my-1 overflow-hidden">
      <button
        data-testid="tool-header"
        className="w-full flex items-center gap-2 px-3 py-1.5 hover:bg-[#252525] cursor-pointer text-left"
        onClick={() => setExpanded(v => !v)}
      >
        {expanded ? (
          <CaretDown size={12} className="text-[#777] flex-shrink-0" />
        ) : (
          <CaretRight size={12} className="text-[#777] flex-shrink-0" />
        )}
        <Wrench size={16} data-testid="tool-icon-wrench" className="text-[#aaa] flex-shrink-0" />
        <span className="text-[#ddd] font-semibold">{tool}</span>
        {summary && (
          <span className="text-[#888] truncate flex-1 min-w-0">{summary}</span>
        )}
      </button>
      {expanded && (
        <div data-testid="tool-detail" className="border-t border-[#2a2a2a] px-3 py-2 bg-[#161616]">
          <pre className="text-xs text-[#aaa] whitespace-pre-wrap break-all overflow-auto max-h-60">
            {JSON.stringify(input, null, 2)}
          </pre>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/ToolCallBlock.test.tsx`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/ToolCallBlock.tsx spa/src/components/ToolCallBlock.test.tsx
git commit -m "feat: ToolCallBlock — unified Wrench icon"
```

---

### Task 4: SPA — ThinkingBlock component

**Files:**
- Create: `spa/src/components/ThinkingBlock.tsx`
- Create: `spa/src/components/ThinkingBlock.test.tsx`

Collapsible thinking block with `Brain` icon from Phosphor. Collapsed by default, shows "Thinking..." header. Expands to show thinking text in monospace.

- [ ] **Step 1: Write failing tests**

```tsx
// spa/src/components/ThinkingBlock.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import ThinkingBlock from './ThinkingBlock'

beforeEach(() => { cleanup() })

describe('ThinkingBlock', () => {
  it('renders collapsed by default showing Thinking header', () => {
    render(<ThinkingBlock content="Let me analyze..." />)
    expect(screen.getByText('Thinking...')).toBeInTheDocument()
    expect(screen.queryByTestId('thinking-content')).toBeNull()
  })

  it('expands on click to show thinking content', () => {
    render(<ThinkingBlock content="Let me analyze this problem." />)
    fireEvent.click(screen.getByTestId('thinking-header'))
    expect(screen.getByTestId('thinking-content')).toHaveTextContent('Let me analyze this problem.')
  })

  it('collapses again on second click', () => {
    render(<ThinkingBlock content="content" />)
    fireEvent.click(screen.getByTestId('thinking-header'))
    expect(screen.getByTestId('thinking-content')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('thinking-header'))
    expect(screen.queryByTestId('thinking-content')).toBeNull()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/ThinkingBlock.test.tsx`
Expected: FAIL (file not found)

- [ ] **Step 3: Implement ThinkingBlock**

```tsx
// spa/src/components/ThinkingBlock.tsx
import { useState } from 'react'
import { Brain, CaretRight, CaretDown } from '@phosphor-icons/react'

interface Props {
  content: string
}

export default function ThinkingBlock({ content }: Props) {
  const [expanded, setExpanded] = useState(false)

  return (
    <div className="border-l-2 border-[#444] my-1">
      <button
        data-testid="thinking-header"
        className="flex items-center gap-2 px-2.5 py-1.5 text-xs text-[#888] hover:text-[#bbb] cursor-pointer w-full text-left"
        onClick={() => setExpanded(v => !v)}
      >
        <Brain size={14} />
        <span>Thinking...</span>
        <span className="ml-auto">
          {expanded ? <CaretDown size={10} /> : <CaretRight size={10} />}
        </span>
      </button>
      {expanded && (
        <div
          data-testid="thinking-content"
          className="px-2.5 pb-2 text-xs text-[#999] leading-relaxed whitespace-pre-wrap font-mono"
        >
          {content}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/ThinkingBlock.test.tsx`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/ThinkingBlock.tsx spa/src/components/ThinkingBlock.test.tsx
git commit -m "feat: ThinkingBlock — collapsible thinking display"
```

---

### Task 5: SPA — ToolResultBlock component

**Files:**
- Create: `spa/src/components/ToolResultBlock.tsx`
- Create: `spa/src/components/ToolResultBlock.test.tsx`

Collapsible block for `tool_result` content blocks in user messages. Shows tool name + success/error state in header, content in collapsed detail.

- [ ] **Step 1: Write failing tests**

```tsx
// spa/src/components/ToolResultBlock.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import ToolResultBlock from './ToolResultBlock'

beforeEach(() => { cleanup() })

describe('ToolResultBlock', () => {
  it('renders collapsed success state', () => {
    render(<ToolResultBlock content="output text" isError={false} />)
    expect(screen.getByTestId('tool-result-header')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-result-content')).toBeNull()
  })

  it('expands to show content on click', () => {
    render(<ToolResultBlock content="command output here" isError={false} />)
    fireEvent.click(screen.getByTestId('tool-result-header'))
    expect(screen.getByTestId('tool-result-content')).toHaveTextContent('command output here')
  })

  it('renders error state with different styling', () => {
    const { container } = render(<ToolResultBlock content="error msg" isError={true} />)
    const block = container.querySelector('[data-testid="tool-result-block"]')
    expect(block?.className).toContain('border-[#302a2a]')
  })

  it('truncates long content in header summary', () => {
    const longContent = 'a'.repeat(200)
    render(<ToolResultBlock content={longContent} isError={false} />)
    const header = screen.getByTestId('tool-result-header')
    expect(header.textContent!.length).toBeLessThan(150)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/ToolResultBlock.test.tsx`
Expected: FAIL (file not found)

- [ ] **Step 3: Implement ToolResultBlock**

```tsx
// spa/src/components/ToolResultBlock.tsx
import { useState } from 'react'
import { CheckCircle, XCircle, CaretRight, CaretDown } from '@phosphor-icons/react'

interface Props {
  content: string
  isError: boolean
}

export default function ToolResultBlock({ content, isError }: Props) {
  const [expanded, setExpanded] = useState(false)
  const summary = content.slice(0, 80) + (content.length > 80 ? '...' : '')

  return (
    <div
      data-testid="tool-result-block"
      className={`rounded-lg border my-1 overflow-hidden ${
        isError ? 'border-[#302a2a] bg-[#1f1b1b]' : 'border-[#2a302a] bg-[#1b1f1b]'
      }`}
    >
      <button
        data-testid="tool-result-header"
        className={`w-full flex items-center gap-2 px-3 py-1.5 cursor-pointer text-left text-xs ${
          isError ? 'text-[#c77] hover:bg-[#251f1f]' : 'text-[#8bc] hover:bg-[#1f251f]'
        }`}
        onClick={() => setExpanded(v => !v)}
      >
        {expanded ? <CaretDown size={10} /> : <CaretRight size={10} />}
        {isError ? <XCircle size={14} /> : <CheckCircle size={14} />}
        <span className="truncate flex-1">{summary}</span>
      </button>
      {expanded && (
        <div
          data-testid="tool-result-content"
          className={`border-t px-3 py-2 text-xs font-mono whitespace-pre-wrap max-h-60 overflow-auto ${
            isError ? 'border-[#302a2a] text-[#c99]' : 'border-[#2a302a] text-[#9b9]'
          }`}
        >
          {content}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/ToolResultBlock.test.tsx`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/ToolResultBlock.tsx spa/src/components/ToolResultBlock.test.tsx
git commit -m "feat: ToolResultBlock — collapsible tool result display"
```

---

### Task 6: SPA — ConversationView wiring (all block types)

**Files:**
- Modify: `spa/src/components/ConversationView.tsx`
- Modify: `spa/src/components/ConversationView.test.tsx`

Wire all new components into the message rendering loop:
- Assistant `thinking` blocks → `ThinkingBlock`
- Assistant `tool_use` blocks → `ToolCallBlock` (already exists, just keep)
- Assistant `text` blocks → `MessageBubble` role=assistant (already exists)
- User `text` blocks → `MessageBubble` role=user (already exists)
- User `tool_result` blocks → `ToolResultBlock`
- User slash commands (text starting with `/`) → command bubble style
- User `[Request interrupted by user]` → interrupted style
- Filter: skip user messages where all text blocks are CC markup (already handled by Go parser, but double-check)

- [ ] **Step 1: Write failing tests**

Add to `spa/src/components/ConversationView.test.tsx`:

```tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { useStreamStore } from '../stores/useStreamStore'

beforeEach(() => {
  cleanup()
  useStreamStore.setState({
    sessions: {},
    handoffState: {},
    handoffProgress: {},
    relayStatus: {},
    sessionStatus: {},
  })
})

// Helper to set up connected state with messages
function setupSession(sessionName: string, messages: any[]) {
  useStreamStore.setState({
    sessions: {
      [sessionName]: {
        messages,
        pendingControlRequests: [],
        isStreaming: false,
        conn: null,
        sessionInfo: { ccSessionId: '', model: '' },
        cost: 0,
      },
    },
    handoffState: { [sessionName]: 'connected' },
  })
}

describe('ConversationView message rendering', () => {
  it('renders thinking block for assistant thinking content', async () => {
    setupSession('test', [{
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [
          { type: 'thinking', thinking: 'Let me analyze...' },
          { type: 'text', text: 'Here is my answer.' },
        ],
        stop_reason: 'end_turn',
      },
    }])

    const { default: ConversationView } = await import('./ConversationView')
    render(<ConversationView sessionName="test" />)

    expect(screen.getByTestId('thinking-header')).toBeInTheDocument()
    expect(screen.getByText('Here is my answer.')).toBeInTheDocument()
  })

  it('renders tool_result block for user tool results', async () => {
    setupSession('test', [{
      type: 'user',
      message: {
        role: 'user',
        content: [
          { type: 'tool_result', tool_use_id: 'toolu_01', content: 'file contents here', is_error: false },
        ],
        stop_reason: null,
      },
    }])

    const { default: ConversationView } = await import('./ConversationView')
    render(<ConversationView sessionName="test" />)

    expect(screen.getByTestId('tool-result-header')).toBeInTheDocument()
  })

  it('renders interrupted message with prohibit style', async () => {
    setupSession('test', [{
      type: 'user',
      message: {
        role: 'user',
        content: [{ type: 'text', text: '[Request interrupted by user]' }],
        stop_reason: null,
      },
    }])

    const { default: ConversationView } = await import('./ConversationView')
    render(<ConversationView sessionName="test" />)

    expect(screen.getByTestId('interrupted-msg')).toBeInTheDocument()
  })

  it('renders slash command with command bubble style', async () => {
    setupSession('test', [{
      type: 'user',
      message: {
        role: 'user',
        content: [{ type: 'text', text: '/exit' }],
        stop_reason: null,
      },
    }])

    const { default: ConversationView } = await import('./ConversationView')
    render(<ConversationView sessionName="test" />)

    expect(screen.getByTestId('command-bubble')).toBeInTheDocument()
    expect(screen.getByTestId('command-bubble')).toHaveTextContent('/exit')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/ConversationView.test.tsx`
Expected: FAIL (no thinking-header, no tool-result-header, no interrupted-msg, no command-bubble)

- [ ] **Step 3: Update ConversationView rendering loop**

In `spa/src/components/ConversationView.tsx`, update imports and the message rendering:

```tsx
// Add imports at top:
import ThinkingBlock from './ThinkingBlock'
import ToolResultBlock from './ToolResultBlock'
import { Prohibit, TerminalWindow } from '@phosphor-icons/react'

// Replace the messages.map block (lines 220-251) with:
{messages.map((msg, i) => {
  const key = `${sessionName}-${i}`

  // --- Assistant messages ---
  if (msg.type === 'assistant' && 'message' in msg) {
    const am = msg as AssistantMessage
    return (
      <div key={key}>
        {am.message.content.map((block, j) => {
          if (block.type === 'thinking' && block.thinking) {
            return <ThinkingBlock key={j} content={block.thinking} />
          }
          if (block.type === 'text' && block.text) {
            return <MessageBubble key={j} role="assistant" content={block.text} />
          }
          if (block.type === 'tool_use' && block.name) {
            return <ToolCallBlock key={j} tool={block.name} input={block.input || {}} />
          }
          return null
        })}
      </div>
    )
  }

  // --- User messages ---
  if (msg.type === 'user' && 'message' in msg) {
    const um = msg as UserMessage
    const blocks = um.message.content

    return (
      <div key={key}>
        {blocks.map((block, j) => {
          // Tool result
          if (block.type === 'tool_result') {
            const content = typeof block.content === 'string' ? block.content : JSON.stringify(block.content)
            return <ToolResultBlock key={j} content={content} isError={block.is_error ?? false} />
          }

          // Text blocks
          if (block.type === 'text' && block.text) {
            // Interrupted
            if (block.text === '[Request interrupted by user]') {
              return (
                <div key={j} data-testid="interrupted-msg"
                  className="flex items-center gap-1.5 bg-[#4a3038] rounded-[12px_12px_4px_12px] px-3 py-1.5 text-sm text-[#eaa] italic">
                  <Prohibit size={14} />
                  <span>Request interrupted by user</span>
                </div>
              )
            }

            // Slash command
            if (block.text.startsWith('/')) {
              return (
                <div key={j} className="flex justify-end">
                  <div data-testid="command-bubble"
                    className="flex items-center gap-1.5 bg-[#4a4028] rounded-[12px_12px_4px_12px] px-3 py-1.5 text-[13px] text-[#e0d0a0] italic font-mono">
                    <TerminalWindow size={14} weight="bold" className="text-[#c0a060]" />
                    <span>{block.text}</span>
                  </div>
                </div>
              )
            }

            // Normal user text
            return <MessageBubble key={j} role="user" content={block.text} />
          }

          return null
        })}
      </div>
    )
  }

  return null
})}
```

- [ ] **Step 4: Run all SPA tests**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/ConversationView.tsx spa/src/components/ConversationView.test.tsx
git commit -m "feat: ConversationView — wire all message block types"
```

---

### Task 7: Integration — full test pass + manual verification

**Files:** None (verification only)

- [ ] **Step 1: Run all Go tests**

Run: `go test ./...`
Expected: ALL PASS

- [ ] **Step 2: Run all SPA tests**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 3: Rebuild daemon and restart**

```bash
go build -o bin/tbox ./cmd/tbox
# Restart daemon + SPA dev server
```

- [ ] **Step 4: Manual test — handoff to stream on 客服中心 session**

1. Open SPA → select 客服中心
2. Switch to stream tab → click Handoff
3. Verify: `/exit` shown as yellow-brown command bubble (right side)
4. Verify: "在 Claude Code 中，清除畫面請使用 /clear。" shown as assistant text (left, no bubble)
5. Verify: CC internal markup (`<local-command-caveat>`, `<local-command-stdout>`) NOT visible
6. Verify: `ping` shown as user bubble (right), `pong` as assistant text (left)

- [ ] **Step 5: Commit (if any fixes needed)**

```bash
git commit -m "fix: adjustments from manual testing"
```
