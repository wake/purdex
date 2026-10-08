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
  const { seats, beadHost, layout, groupStyle, sidebarStyle, namesOff, target, log } = useProtoTeam()
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
        <span className="ml-2 text-text-muted">team 介面 · 第三版</span>
        <button type="button" onClick={() => setOpen(false)} className="ml-auto text-text-muted hover:text-text-primary cursor-pointer">收起</button>
      </div>
      <Seg label="分頁位置" value={tabPosition} options={[['top', '上方'], ['left', '左側'], ['both', '兩側']]} onChange={(v) => useLayoutStore.getState().setTabPosition(v)} />
      <Seg label="深淺色" value={theme === 'light' ? 'light' : 'dark'} options={[['dark', '深色'], ['light', '淺色']]} onChange={(v) => useThemeStore.getState().setActiveTheme(v)} />
      <div className="rounded-lg border border-dashed border-border-default px-2 py-1.5 flex flex-col gap-1">
        <div className="text-[10.5px] text-text-muted">使用者設定（模擬「設定 → 介面 → 分頁」，存在這台機器）</div>
        <Seg label="顆粒主機" value={beadHost ? 'on' : 'off'} options={[['off', '只有 bot'], ['on', 'bot＋主機圖示']]} onChange={(v) => useProtoTeam.setState({ beadHost: v === 'on' })} />
      </div>
      <div className="text-[10.5px] text-text-muted font-semibold pt-0.5">比較用（之後定一種）</div>
      <Seg label="群組樣式" value={groupStyle} options={[['tint', '標籤＋淡色底'], ['frame', '外框'], ['topbar', '頂端色條'], ['plate', '共用底板'], ['label', '只有標籤']]} onChange={(v) => useProtoTeam.setState({ groupStyle: v })} />
      <Seg label="側欄符號" value={sidebarStyle} options={[['hook', '⎿ 掛勾'], ['plusminus', '⊟／⊞'], ['chevron', '▾＋⎿']]} onChange={(v) => useProtoTeam.setState({ sidebarStyle: v })} />
      <Seg label="team 名" value={namesOff ? 'off' : 'on'} options={[['on', '有名字'], ['off', '沒名字（看退回）']]} onChange={(v) => useProtoTeam.setState({ namesOff: v === 'off' })} />
      <Seg label="面板排法" value={layout} options={[['a', 'A 依序'], ['b', 'B 模型到第一行'], ['c', 'C 環形 context']]} onChange={(v) => useProtoTeam.setState({ layout: v })} />
      <div className="text-[10.5px] text-text-muted pl-[70px] -mt-1 leading-snug">
        {layout === 'a' && '第二行：模型＋effort · context % · 任務摘要'}
        {layout === 'b' && '模型圖示挪到第一行尾；第二行：context % · 任務摘要'}
        {layout === 'c' && '第一行尾是環形 context（中間是模型圖示）；第二行只剩任務摘要'}
      </div>
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
      <div className="text-[11px] text-text-secondary border-t border-border-subtle pt-1.5" data-testid="ctl-log">
        <b className="text-text-primary">最近操作：</b>{log}
      </div>
      <div className="text-[10.5px] text-text-muted leading-snug">
        拖曳：左邊 bot 顆粒、上方 member 分頁、面板的 member 列都能拖，三處順序同步；拖出群組或拖到 lead 前面會彈回。
      </div>
    </div>
  )
}
