// spa/src/proto/team/ProtoControls.tsx — the prototype's control panel (bottom-right, collapsible).
import { useState } from 'react'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useThemeStore } from '../../stores/useThemeStore'
import {
  useProtoTeam, tabOfSeat, openSeat, closeTab, spawn, detach, becomeLead, setLight, setSubagents, stepTab, say,
} from './store'
import { seed } from './seed'

function Seg<T extends string>({ label, value, options, onChange }: { label: string; value: T; options: [T, string][]; onChange: (v: T) => void }) {
  return (
    <div className="flex items-center gap-1.5 flex-wrap">
      <span className="text-[10.5px] text-text-muted w-16 flex-shrink-0">{label}</span>
      <div className="inline-flex rounded-md bg-surface-tertiary p-0.5 flex-wrap">
        {options.map(([v, l]) => (
          <button
            key={v}
            type="button"
            data-testid={`ctl-${label}-${v}`}
            onClick={() => onChange(v)}
            className={`px-2 py-0.5 rounded text-[11.5px] cursor-pointer ${value === v ? 'bg-accent text-white font-semibold' : 'text-text-secondary hover:text-text-primary'}`}
          >
            {l}
          </button>
        ))}
      </div>
    </div>
  )
}

function Btn({ children, onClick, disabled, testId }: { children: React.ReactNode; onClick: () => void; disabled?: boolean; testId?: string }) {
  return (
    <button
      type="button"
      data-testid={testId}
      disabled={disabled}
      onClick={onClick}
      className="px-2 py-0.5 rounded border border-border-default text-[11.5px] hover:bg-surface-hover cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
    >
      {children}
    </button>
  )
}

export function ProtoControls({ stepIds, onOpenPicker }: { stepIds: string[]; onOpenPicker: () => void }) {
  const [open, setOpen] = useState(true)
  const tabPosition = useLayoutStore((s) => s.tabPosition)
  const theme = useThemeStore((s) => s.activeThemeId)
  const { seats, beadHost, groupStyle, sidebarStyle, collapseStyle, hookStyle, openMark, namesOff, target, log } = useProtoTeam()
  const members = Object.values(seats).filter((s) => s.role === 'member' && s.alive && s.teamKey)
  const tgt = target ? seats[target] : null
  const tgtTab = tgt ? tabOfSeat(tgt) : null
  const c4 = seats['c4']

  if (!open) {
    return (
      <button type="button" data-testid="ctl-open" onClick={() => setOpen(true)} className="fixed right-3 bottom-3 z-50 px-3 py-1.5 rounded-full bg-accent text-white text-xs shadow-lg cursor-pointer">
        原型控制
      </button>
    )
  }
  return (
    <div data-testid="proto-controls" className="fixed right-3 bottom-3 z-50 w-[min(380px,calc(100vw-24px))] max-h-[48vh] overflow-y-auto rounded-xl border border-border-default bg-surface-elevated shadow-2xl p-3 text-xs flex flex-col gap-2">
      <div className="flex items-center">
        <span className="font-semibold">原型控制</span>
        <span className="ml-2 text-text-muted">team 介面 · 第四版</span>
        <button type="button" onClick={() => setOpen(false)} className="ml-auto text-text-muted hover:text-text-primary cursor-pointer">收起</button>
      </div>
      <Seg label="分頁位置" value={tabPosition} options={[['top', '上方'], ['left', '左側'], ['both', '兩側']]} onChange={(v) => useLayoutStore.getState().setTabPosition(v)} />
      <Seg label="深淺色" value={theme === 'light' ? 'light' : 'dark'} options={[['dark', '深色'], ['light', '淺色']]} onChange={(v) => useThemeStore.getState().setActiveTheme(v)} />
      <div className="rounded-lg border border-dashed border-border-default px-2 py-1.5 flex flex-col gap-1">
        <div className="text-[10.5px] text-text-muted">使用者設定（模擬「設定 → 介面 → 分頁」，存在這台機器）</div>
        <Seg label="顆粒主機" value={beadHost ? 'on' : 'off'} options={[['off', '只有 bot'], ['on', 'bot＋主機圖示']]} onChange={(v) => useProtoTeam.setState({ beadHost: v === 'on' })} />
      </div>
      <div className="text-[10.5px] text-text-muted font-semibold pt-0.5">比較用（之後定一種）</div>
      <Seg label="群組樣式" value={groupStyle} options={[['label', '只有標籤'], ['dot', '色點'], ['endcap', '收尾刻度'], ['gap', '間距分群'], ['sepcolor', '色分隔線'], ['rule', '細線'], ['combo', '色點＋間距＋收尾']]} onChange={(v) => useProtoTeam.setState({ groupStyle: v })} />
      <Seg label="舊群組" value={groupStyle} options={[['tint', '淡色底'], ['frame', '外框'], ['topbar', '頂端色條'], ['plate', '共用底板']]} onChange={(v) => useProtoTeam.setState({ groupStyle: v })} />
      <Seg label="收起樣式" value={collapseStyle} options={[['users', '新：人群圖示＋燈點'], ['sign', '舊：符號＋「N 個收起」']]} onChange={(v) => useProtoTeam.setState({ collapseStyle: v })} />
      <Seg label="掛勾" value={hookStyle} options={[['thin', '細線圓角'], ['bold', '加粗'], ['rail', '樹狀刻度'], ['glyph', '⎿ 字元']]} onChange={(v) => useProtoTeam.setState({ hookStyle: v })} />
      <Seg label="開分頁標示" value={openMark} options={[['tick', '底部小點'], ['none', '不標示']]} onChange={(v) => useProtoTeam.setState({ openMark: v })} />
      {collapseStyle === 'sign' && (
        <Seg label="舊符號" value={sidebarStyle} options={[['hook', '⎿ 掛勾'], ['plusminus', '⊟／⊞'], ['chevron', '▾＋⎿']]} onChange={(v) => useProtoTeam.setState({ sidebarStyle: v })} />
      )}
      <Seg label="team 名" value={namesOff ? 'off' : 'on'} options={[['on', '有名字'], ['off', '沒名字（看退回）']]} onChange={(v) => useProtoTeam.setState({ namesOff: v === 'off' })} />
      <div className="flex items-center gap-1.5 flex-wrap">
        <span className="text-[10.5px] text-text-muted w-16">切換分頁</span>
        <Btn testId="ctl-prev" onClick={() => stepTab(-1, stepIds)}>◀ 上一個</Btn>
        <Btn testId="ctl-next" onClick={() => stepTab(1, stepIds)}>下一個 ▶</Btn>
        <Btn testId="ctl-picker" onClick={onOpenPicker}>session 清單</Btn>
      </div>
      <div className="flex items-center gap-1.5 flex-wrap">
        <span className="text-[10.5px] text-text-muted w-16">事件</span>
        <Btn testId="ctl-spawn" onClick={spawn}>＋ spawn member</Btn>
        <Btn testId="ctl-lead2" onClick={() => becomeLead('c4')} disabled={!c4 || !!c4.teamKey}>nexen-c4 成為 lead</Btn>
        <Btn testId="ctl-reset" onClick={() => { seed(); say('重設。') }}>重設</Btn>
      </div>
      <div className="flex items-center gap-1.5 flex-wrap">
        <span className="text-[10.5px] text-text-muted w-16">對象</span>
        <select
          data-testid="ctl-target"
          value={target ?? ''}
          onChange={(e) => useProtoTeam.setState({ target: e.target.value })}
          className="bg-surface-input border border-border-default rounded px-1 py-0.5 max-w-[200px]"
        >
          {members.length === 0 && <option value="">（沒有 member）</option>}
          {members.map((m) => <option key={m.sessionId} value={m.sessionId}>{m.title}</option>)}
        </select>
      </div>
      <div className="flex items-center gap-1.5 flex-wrap pl-[70px]">
        <Btn testId="ctl-kill" disabled={!tgt} onClick={() => tgt && detach(tgt.sessionId, 'kill')}>kill</Btn>
        <Btn testId="ctl-release" disabled={!tgt} onClick={() => tgt && detach(tgt.sessionId, 'release')}>release</Btn>
        <Btn testId="ctl-tab" disabled={!tgt} onClick={() => { if (!tgt) return; if (tgtTab) closeTab(tgtTab); else openSeat(tgt.teamKey, tgt.sessionId) }}>{tgtTab ? '關分頁' : '開分頁'}</Btn>
      </div>
      <div className="flex items-center gap-1.5 flex-wrap pl-[70px]">
        <Btn testId="ctl-st-waiting" disabled={!tgt} onClick={() => tgt && setLight(tgt.sessionId, 'waiting')}>等你回答</Btn>
        <Btn testId="ctl-st-running" disabled={!tgt} onClick={() => tgt && setLight(tgt.sessionId, 'running')}>工作中</Btn>
        <Btn testId="ctl-st-idle" disabled={!tgt} onClick={() => tgt && setLight(tgt.sessionId, 'idle')}>閒置</Btn>
        <Btn testId="ctl-st-error" disabled={!tgt} onClick={() => tgt && setLight(tgt.sessionId, 'error')}>失敗</Btn>
        <Btn testId="ctl-st-unread" disabled={!tgt} onClick={() => tgt && setLight(tgt.sessionId, 'unread')}>未讀</Btn>
      </div>
      <div className="flex items-center gap-1.5 flex-wrap pl-[70px]">
        <span className="text-[10.5px] text-text-muted">subagent</span>
        {[0, 1, 2, 3].map((n) => <Btn key={n} testId={`ctl-sub-${n}`} disabled={!tgt} onClick={() => tgt && setSubagents(tgt.sessionId, n)}>{n}</Btn>)}
      </div>
      <details data-testid="ctl-notes" className="text-[11px] text-text-secondary border-t border-border-subtle pt-1.5" open>
        <summary className="cursor-pointer font-semibold text-text-primary">第四版說明與待確認</summary>
        <div className="mt-1 flex flex-col gap-1 leading-snug">
          <div><b>這版改了</b>：①多排顆粒的掛勾一路到最後一排（四種畫法都支援） ②顆粒不再淡出，正在看的 member 是 active（亮字＋底），其餘一律 inactive ③顆粒預設「bot＋主機圖示」 ④新收起樣式（前面沒有箭頭；收起＝人群圖示＋每人一顆燈點，整行點開；展開時點掛勾或顆粒旁空白處收起） ⑤面板固定 C 排法、active 跟側欄同一種、不顯示主機名、後方標記與摘要調亮 ⑥面板拿掉頂端色條、兩種模式同寬、縮成一行會換排 ⑦上方群組新增 6 種低干擾樣式。</div>
          <div className="font-semibold text-text-primary pt-0.5">待確認</div>
          <div>1. 有沒有開分頁：我做成顆粒底部一個 3px 的 team 色小點（「開分頁標示」可關掉＝完全不標示）。面板的「未開」小字照留。要保留、關掉、還是換別的？</div>
          <div>2. 掛勾替代：細線圓角（最接近 App 圖示線條）／加粗（較有存在感）／樹狀刻度（每排各一個刻度，像 ├ └）；⎿ 字元版保留對照，多排時補了一條豎線。</div>
          <div>3. 群組樣式（都以「只有標籤」為底）：色點＝每個 member／lead 分頁圖示左上角一顆 team 色點，分頁本身不動；收尾刻度＝最後一個分頁後面一根短的 team 色小豎條，看得出群組在哪結束；間距分群＝群組前後多留空、群組內分頁貼緊，不加任何顏色；色分隔線＝群組內分頁之間的分隔線改 team 色；細線＝整個群組底下一條很淡的連續線（不是每個分頁一條底線）；色點＋間距＋收尾＝我個人推薦的組合，三個都很輕。舊的四種留在「舊群組」當對照。</div>
          <div>4. 收起狀態的燈點用主燈號四色（綠／黃／灰／紅），沒有燈號的是淺灰空點；未讀不另外標。</div>
          <div>5. 面板縮成一行：寬度固定 312，名字最多 84px，顆粒放不下就換到第二排，展開鈕固定在右上。</div>
        </div>
      </details>
      <div className="text-[11px] text-text-secondary border-t border-border-subtle pt-1.5" data-testid="ctl-log">
        <b className="text-text-primary">最近操作：</b>{log}
      </div>
      <div className="text-[10.5px] text-text-muted leading-snug">
        拖曳：左邊 bot 顆粒、上方 member 分頁、面板的 member 列都能拖，三處順序同步；拖出群組或拖到 lead 前面會彈回。
      </div>
    </div>
  )
}
