import type { IBufferLine } from '@xterm/xterm'

/**
 * 把 translateToString 的 JS 字串 offset 轉成 terminal cell column。
 *
 * 問題背景：matcher 拿到的是 translateToString(true) 結果，其 index 是 JS UTF-16
 * offset；但 xterm ILink.range 要的是 terminal cell index。CJK / emoji 等寬字元
 * 在 terminal 佔 2 cell、在 JS 佔 1–2 char，兩者不一致。
 *
 * 作法：走訪 buffer cell（唯一權威），建立 (jsOffset → col) 的遞增表，
 * 再以 binary search 回答任意 offset。
 */
export function buildJsOffsetToCol(line: IBufferLine): (jsOffset: number) => number {
  // entries[i] = [jsOffset_at_start_of_cell, col_of_cell]；js 單調遞增
  const entries: Array<[number, number]> = []
  let js = 0
  for (let x = 0; x < line.length; x++) {
    const cell = line.getCell(x)
    // width 0 = 寬字元的第二格（continuation），跳過不記錄
    const w = cell?.getWidth() ?? 1
    if (w === 0) continue
    entries.push([js, x])
    const chars = cell?.getChars() || ' '
    js += chars.length
  }
  // 哨兵：end of line
  entries.push([js, line.length])

  return (jsOffset: number): number => {
    // Binary search：找第一個 entries[i].js >= jsOffset；對應 col 即為答案
    // 若 jsOffset 超出所有 entry，回傳哨兵 col = line.length（clamp 行為）
    let lo = 0
    let hi = entries.length - 1
    while (lo < hi) {
      const mid = (lo + hi) >> 1
      if (entries[mid][0] < jsOffset) lo = mid + 1
      else hi = mid
    }
    return entries[lo][1]
  }
}
