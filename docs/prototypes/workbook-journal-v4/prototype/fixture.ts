// Fixture for the v4 prototype: written fresh (the 10-14 interface line), manager-report tone. No secrets.
import type { Entry, Feature, Task } from '../../src/components/journal-v4-proto/types'

export const team = {
  name: '介面整合（B 線）',
  label: 'B 線',
  cap: 3,
  lead: { title: 'purdex-88-b8', address: 'mlab/purdex-88-b8' },
  members: [
    { title: 'purdex-53-pe', ref: '_53pe01', model: 'Sonnet', effort: 'medium', ctx: 38 },
    { title: 'purdex-ios-28-6c', ref: '_286c02', model: 'Sonnet', effort: 'medium', ctx: 52 },
    { title: 'purdex-9a-ui', ref: '_9aui03', model: 'Sonnet', effort: 'low', ctx: 19 },
  ],
}

const q = (...t: string[]) => t.map((x) => ({ t: x, done: false }))
const ticked = (...t: string[]) => t.map((x) => ({ t: x, done: true }))
const D = '2026-10-14'
const P = '2026-10-13'
const L = 'purdex-88-b8'

export const teamEntries: Entry[] = [
  { id: 'a1', date: P, time: '16:20', kind: 'exec', actor: L, text: '我把 QR 配對與快速回覆的真機驗收結果整理成清單，交給使用者逐項確認。', refs: [], pinned: false },
  { id: 'a2', date: P, time: '17:05', kind: 'comm', actor: 'purdex-ios-28-6c → purdex-88-b8', text: 'iOS 0.6.73 已經上傳，請 88 提醒使用者換 OTA。', refs: [], pinned: false },
  { id: 'a3', date: P, time: '18:40', kind: 'exec_done', actor: L, text: '群組整組拖曳的規格已寫完並送審，等 codex 回覆。', refs: ['#2766'], pinned: false },
  { id: 'b1', date: D, time: '09:05', kind: 'request', by: 'user', actor: '使用者 → purdex-88-b8', text: 'member 的「逾時沒人回應」暫停，按解除沒有反應，請你查清楚。', refs: ['#2816'], pinned: false },
  { id: 'b2', date: D, time: '09:12', kind: 'exec', actor: L, text: '我依 relay v2 的 plan 定案：「解除」等於拒絕接力，daemon 那一半交給 A 線處理。', refs: [], pinned: false },
  { id: 'b3', date: D, time: '09:20', kind: 'task_new', actor: 'purdex-88-b8 → purdex-53-pe', text: '#12 Mac 的 relay v2 App 契約（RA-1、RA-2）已派給 purdex-53-pe。', refs: ['#12'], taskId: '7bcf08-12', pinned: false },
  { id: 'b4', date: D, time: '09:48', kind: 'comm', actor: 'purdex-1f-vq → purdex-88-b8', text: 'alpha.734 已經部署到兩台機器，可以請使用者做真機驗收。', refs: [], pinned: false },
  { id: 'b5', date: D, time: '10:15', kind: 'request', by: 'member', actor: 'purdex-53-pe → purdex-88-b8', text: 'J-0a 的接續讀取位置會漏資料，請 lead 決定要怎麼改。', refs: ['J-0a'], pinned: false },
  { id: 'b6', date: D, time: '10:40', kind: 'exec_done', actor: L, text: 'RA-2 已經 merge：拿掉「不再詢問」，額度說明改成暫停的說法。', refs: ['PR #2829'], pinned: false },
  { id: 'b7', date: D, time: '10:41', kind: 'task_done', actor: 'purdex-53-pe', text: '#12 RA-1、RA-2 都完成並 merge，relay v2 的 App 契約收尾。', refs: ['#12', 'PR #2829'], taskId: '7bcf08-12', pinned: false },
  { id: 'b8', date: D, time: '11:02', kind: 'task_new', actor: 'purdex-88-b8 → purdex-53-pe', text: '#15 #2777 的純搬移已派出，要求用逐宣告位元組比對證明。', refs: ['#15', '#2777'], taskId: '7bcf08-15', pinned: false },
  { id: 'b9', date: D, time: '11:30', kind: 'task_done', actor: 'purdex-53-pe', text: '#15 純搬移完成，位元組比對沒有差異，已 merge。', refs: ['#15', 'PR #2834'], taskId: '7bcf08-15', pinned: false },
  { id: 'b10', date: D, time: '11:45', kind: 'task_new', actor: 'purdex-88-b8 → purdex-53-pe', text: '#16 日誌 J-0a（daemon 的資料來源）已建立，目前先暫停等規格改版。', refs: ['#16', 'J-0a'], taskId: '7bcf08-16', pinned: false },
  { id: 'b11', date: D, time: '11:46', kind: 'task_new', actor: 'purdex-88-b8 → purdex-9a-ui', text: '#17 日誌 J-0b 已建立，等 J-0a 有結論再開工。', refs: ['#17', 'J-0b'], taskId: '7bcf08-17', pinned: false },
  {
    id: 'c1', date: D, time: '12:30', kind: 'confirm', actor: 'purdex-88-b8 → 使用者',
    text: '日誌的寫法與存放有六件事需要你決定，已全部回覆。', refs: [], pinned: false,
    items: ticked('Haiku 怎麼寫', '「由 leader 記錄」是哪一種', '待確認何時出、何時算答完', '檔案放哪裡', '任務檔與路線圖誰維護', '姓名池'),
    detail: '一共六項，一項一項問你：\n1. 日誌由 Haiku 怎麼寫？沿用現在每個回合呼叫一次的做法，還是另開一個呼叫？\n2. 「由 leader 記錄」是指整個 team 一份日誌，還是每位 member 各一份再合併？\n3. 待確認什麼時候出現，什麼時候算你答完了？\n4. 日誌檔放在專案資料夾，還是放在 daemon 自己的資料夾？\n5. 任務檔與路線圖由誰維護？\n6. 姓名池要不要一起做？',
  },
  {
    id: 'c2', date: D, time: '13:10', kind: 'confirm', actor: 'purdex-88-b8 → 使用者',
    text: '規格 v4 還有四個方向需要你點頭，已全部回覆。', refs: [], pinned: false,
    items: ticked('日誌檔要不要進 git', '單一 session 的任務從哪來', '路線圖誰維護', '待確認誰來勾'),
    detail: '規格 v4 寫完了，有四件事想跟你確認：\n1. 日誌檔要不要進 git？\n2. 沒有 team 的單一 session，它的任務從哪裡來？\n3. 路線圖由誰維護？\n4. 待確認的 checkbox 由誰來勾？',
  },
  {
    id: 'c3', date: D, time: '14:05', kind: 'confirm', actor: 'purdex-88-b8 → 使用者',
    text: '規格 v4 剩下四件事需要你決定，目前都還沒有回覆。', refs: [], pinned: true,
    items: q('檔案怎麼進 git（專用分支／main／直接 commit）', '日誌按鈕上的數字', '篩選分類', '舊工作簿條文替換'),
    detail: '想請你決定下面四件事，我才能把規格送審：\n\n1. 日誌檔怎麼進 git？\n　甲：專用分支 purdex/journal，由 daemon 每十分鐘推一次，main 不受影響。\n　乙：放在 main 的 .purdex/ 底下，隨每次 bump PR 一起進。\n　丙：daemon 直接 commit 到 main（需要你明確授權）。\n2. 日誌按鈕上的數字，我提議顯示「目前未答完的待確認項目數」，一般事件不算。可以嗎？\n3. 篩選分類，我提議：全部／執行／任務／請求與溝通／待確認。可以嗎？\n4. 舊工作簿規格第 10 章的條文要換成新的，換法在規格第 12 章，請你過目。',
  },
]

export const teamTasks: Task[] = [
  { id: '7bcf08-18', no: '#2793', title: 'badge 推播加單調 revision', owner: 'purdex-ios-28-6c', state: 'doing', refs: ['#2793'] },
  { id: '7bcf08-19', no: '#2816', title: '逾時暫停按「解除」沒有反應', owner: 'purdex-9a-ui', state: 'doing', refs: ['#2816'] },
  { id: '7bcf08-16', no: '#16', title: '日誌 J-0a', owner: 'purdex-53-pe', state: 'paused' },
  { id: '7bcf08-17', no: '#17', title: '日誌 J-0b', owner: 'purdex-9a-ui', state: 'todo' },
  { id: '7bcf08-15', no: '#15', title: '#2777 純搬移', owner: 'purdex-53-pe', state: 'done', refs: ['PR #2834'] },
  { id: '7bcf08-12', no: '#12', title: 'RA-1／RA-2（relay v2 App 契約）', owner: 'purdex-53-pe', state: 'done', refs: ['PR #2829'] },
]

export const roadmap: Feature[] = [
  { name: '工作簿日誌', rows: [
    { stage: 'J-P', desc: 'prompt 實測（真實回合 30 筆）', status: '進行中' },
    { stage: 'J-1', desc: 'daemon：檔案寫入、API、git', status: '未開始' },
    { stage: 'J-2', desc: 'Mac：日誌卡、任務與路線圖分頁', status: '未開始' },
    { stage: 'J-3', desc: 'iOS：日誌分頁', status: '未開始' },
  ] },
  { name: 'relay v2 App 契約', rows: [
    { stage: 'RA-1', desc: '暫停與解除的 App 端流程', status: '完成' },
    { stage: 'RA-2', desc: '拿掉「不再詢問」，額度說明改寫', status: '完成' },
    { stage: 'RI-1…RI-3', desc: 'iOS 對應改動', status: '完成' },
    { stage: '真機驗收', desc: 'Mac 與 iOS 各走一遍', status: '審查中' },
  ] },
  { name: 'App 角標', rows: [
    { stage: 'BG-1…BG-5', desc: '角標規則與推播', status: '完成' },
    { stage: '真機驗收', desc: '兩台機器實測', status: '審查中' },
  ] },
]

// A separate, small fixture for a solo session (the summariser's todos are its tasks).
export const solo = {
  name: 'purdex-ios-28-6c',
  entries: [
    { id: 's1', date: D, time: '09:30', kind: 'request', by: 'user', actor: '使用者 → purdex-ios-28-6c', text: '請把 App 角標的數字改成只算還沒讀的提示。', refs: ['#2793'], pinned: false },
    { id: 's2', date: D, time: '09:42', kind: 'task_new', actor: 'purdex-ios-28-6c', text: '#1 角標只算未讀提示，已列入待辦。', refs: ['#1'], taskId: '1', pinned: false },
    { id: 's3', date: D, time: '09:43', kind: 'task_new', actor: 'purdex-ios-28-6c', text: '#2 推播加單調 revision，避免舊通知蓋掉新的。', refs: ['#2', '#2793'], taskId: '2', pinned: false },
    { id: 's4', date: D, time: '09:44', kind: 'task_new', actor: 'purdex-ios-28-6c', text: '#3 順便清掉舊的角標快取。', refs: ['#3'], taskId: '3', pinned: false },
    { id: 's5', date: D, time: '10:20', kind: 'exec', actor: 'purdex-ios-28-6c', text: '我改完角標的計算，測試全部通過。', refs: [], pinned: false },
    { id: 's6', date: D, time: '10:21', kind: 'task_done', actor: 'purdex-ios-28-6c', text: '#1 角標只算未讀提示，已完成。', refs: ['#1'], taskId: '1', pinned: false },
    {
      id: 's7', date: D, time: '10:50', kind: 'confirm', actor: 'purdex-ios-28-6c → 使用者',
      text: '角標的細節有兩件事要你決定，目前還沒有回覆。', refs: [], pinned: true,
      items: q('被隱藏的分頁敲門要不要算進角標', '角標超過 99 時顯示 99+ 還是完整數字'),
      detail: '角標還有兩個細節：\n1. 被隱藏的分頁敲門，要不要算進角標數字？\n2. 數字超過 99 時，顯示 99+，還是顯示完整的數字？',
    },
  ] as Entry[],
  tasks: [
    { id: '2', no: '2', title: '推播加單調 revision', owner: 'purdex-ios-28-6c', state: 'doing' },
    { id: '1', no: '1', title: '角標只算未讀提示', owner: 'purdex-ios-28-6c', state: 'done' },
    { id: '3', no: '3', title: '清掉舊的角標快取', owner: 'purdex-ios-28-6c', state: 'dropped' },
  ] as Task[],
}

// Entries the 模擬新事件 button appends, in turn.
export const simEntries: Omit<Entry, 'id' | 'date' | 'time'>[] = [
  { kind: 'exec', actor: L, text: '我把 J-0a 的接續讀取位置改成往回多讀一段，並請 purdex-53-pe 重跑測試。', refs: ['J-0a'], pinned: false },
  { kind: 'comm', actor: 'purdex-ios-28-6c → purdex-88-b8', text: 'iOS 的角標真機驗收已跑完，結果在 #2793。', refs: ['#2793'], pinned: false },
  {
    kind: 'confirm', actor: 'purdex-88-b8 → 使用者', text: '日誌的第一版要先做哪一端，需要你決定。', refs: [], pinned: true,
    items: q('先做 Mac 還是先做 iOS', '第一版要不要附路線圖'),
    detail: '日誌第一版的順序想請你決定：\n1. 先做 Mac，還是先做 iOS？\n2. 第一版要不要連路線圖分頁一起做？',
  },
]
