import type { Terminal, ILinkProvider, ILink } from '@xterm/xterm'
import type { TerminalLinkRegistry, LinkContext } from './types'

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
      if (!text) { callback([]); return }

      const links: ILink[] = []
      for (const matcher of registry.getMatchers()) {
        for (const raw of matcher.provide(text)) {
          const token = {
            type: matcher.type,
            text: raw.text,
            range: raw.range,
            meta: raw.meta,
          }
          links.push({
            // xterm IBufferCellPosition 是 1-indexed inclusive；我們的 endCol 是 0-indexed exclusive，兩者數值相同
            range: {
              start: { x: raw.range.startCol + 1, y: bufferLineNumber },
              end:   { x: raw.range.endCol,       y: bufferLineNumber },
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
