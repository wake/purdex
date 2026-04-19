import type { Terminal, ILinkProvider, ILink } from '@xterm/xterm'
import type { TerminalLinkRegistry, LinkContext } from './types'
import { buildJsOffsetToCol } from './col-map'

export function createXtermLinkProvider(
  registry: TerminalLinkRegistry,
  getCtx: () => LinkContext,
  term: Terminal,
): ILinkProvider {
  return {
    provideLinks(bufferLineNumber: number, callback: (links: ILink[] | undefined) => void): void {
      const line = term.buffer.active.getLine(bufferLineNumber - 1)
      // trimRight=true 去掉終端右側填充空白，避免誤導後續 matcher 的 column 計算
      const text = line?.translateToString(true) ?? ''
      if (!text || !line) { callback([]); return }

      // CJK / emoji 寬字元修正：matcher 回傳的 startCol/endCol 是 JS 字串 offset，
      // xterm 需要的是 terminal cell column。這裡一次建表給後續所有 matcher 重用。
      const offsetToCol = buildJsOffsetToCol(line)

      const links: ILink[] = []
      for (const matcher of registry.getMatchers()) {
        for (const raw of matcher.provide(text)) {
          const startCol = offsetToCol(raw.range.startCol)
          const endCol = offsetToCol(raw.range.endCol)
          const token = {
            type: matcher.type,
            text: raw.text,
            range: { startCol, endCol },
            meta: raw.meta,
          }
          links.push({
            // xterm IBufferCellPosition 是 1-indexed inclusive；我們的 endCol 是 0-indexed exclusive，兩者數值相同
            range: {
              start: { x: startCol + 1, y: bufferLineNumber },
              end:   { x: endCol,       y: bufferLineNumber },
            },
            text: raw.text,
            activate: (event) => { registry.dispatch(token, getCtx(), event) },
          })
        }
      }
      callback(links)
    },
  }
}
