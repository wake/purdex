import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { TableKit } from '@tiptap/extension-table'
import { TaskList } from '@tiptap/extension-task-list'
import { TaskItem } from '@tiptap/extension-task-item'

// The Live Mode schema, shared by TiptapEditor and by the real-editor round-trip
// tests that drive this exact extension set.
//
// StarterKit has no `table` / `taskList` node, so without TableKit +
// TaskList/TaskItem those markdown tokens have nowhere to land and are dropped at
// PARSE time — a GFM table round-tripped to '' and `- [ ] a` degraded to `- a`.
// The extensions ship their own parseMarkdown / renderMarkdown, which
// MarkdownManager registers straight from this array, so no Markdown.configure
// bridge is needed.
//
// Lives in its own module rather than in TiptapEditor.tsx so that file keeps
// exporting only components (react-refresh/only-export-components).
export const tiptapExtensions = [StarterKit, Markdown, TableKit, TaskList, TaskItem]
