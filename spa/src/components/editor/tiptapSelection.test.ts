import { describe, it, expect } from 'vitest'
import { Schema } from '@tiptap/pm/model'
import { TextSelection, Selection } from '@tiptap/pm/state'
import { resolveRestoreSelection } from './tiptapSelection'

const schema = new Schema({
  nodes: { doc: { content: 'paragraph+' }, paragraph: { content: 'text*' }, text: {} },
})
// <p>hello world</p>  → text 1..12, doc.content.size = 13
function makeDoc(text = 'hello world') {
  return schema.node('doc', null, [schema.node('paragraph', null, text ? [schema.text(text)] : [])])
}

it('preserves a legal range selection (AC6)', () => {
  const doc = makeDoc()
  const sel = resolveRestoreSelection(doc, { from: 1, to: 6 })
  expect(sel).toBeInstanceOf(TextSelection)
  expect(sel.from).toBe(1)
  expect(sel.to).toBe(6)
})

it('clamps an out-of-range selection to a legal inline position (AC7)', () => {
  const doc = makeDoc() // size 13; pos 13 is after the paragraph (parent=doc, not inline)
  expect(() => resolveRestoreSelection(doc, { from: 999, to: 1000 })).not.toThrow()
  const sel = resolveRestoreSelection(doc, { from: 999, to: 1000 })
  // clamp -> 13 (inlineContent false) -> Selection.near -> 12 (inline). Must NOT
  // assert only `to <= size`: create(doc,13,13) returns an illegal 13..13 that
  // also satisfies `<= size`, so that weak assertion would false-green the old
  // try/catch path. Assert the endpoints land in inlineContent instead.
  expect(doc.resolve(sel.from).parent.inlineContent).toBe(true)
  expect(doc.resolve(sel.to).parent.inlineContent).toBe(true)
  expect(sel.from).toBeLessThan(doc.content.size)
})

it('falls back to a legal selection for an illegal (non-textblock) position (AC7)', () => {
  const doc = makeDoc()
  // pos 0 is before the paragraph → resolve(0).parent is the doc node
  // (inlineContent === false) → degrade to Selection.near (NOT via a throw —
  // TextSelection.create would silently return an illegal 0..0 here).
  const sel = resolveRestoreSelection(doc, { from: 0, to: 0 })
  expect(sel).toBeInstanceOf(Selection)
  expect(sel.from).toBeGreaterThanOrEqual(1)
})

it('never throws on an empty doc (AC7)', () => {
  const doc = makeDoc('')
  expect(() => resolveRestoreSelection(doc, { from: 5, to: 9 })).not.toThrow()
})
