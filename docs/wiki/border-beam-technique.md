# Border Beam 技術文件

## 概述

Border Beam 是一種讓短線段沿著圓角矩形邊框軌道移動的動畫效果，用於 workspace icon 的 agent 狀態指示器。線段前端明亮、尾端漸消，形成彗星尾巴般的視覺效果。

此效果又稱為 **Border Beam**、**Traveling Light Border**、**Border Trail**，在 Magic UI、Aceternity UI、Fuselagem UI 等元件庫中均有實作。

## 技術選型

### 評估過的方案

| 方案 | 元素數 | GPU 加速 | 漸層支援 | 結論 |
|------|--------|----------|----------|------|
| SVG `stroke-dashoffset` 多層疊加 | 16 個 `<rect>` | 否（SVG repaint） | 用多段模擬 | 效果好但效能差 |
| CSS `conic-gradient` + `@property` | 1 個 | 否（每幀重繪漸層） | 原生支援 | Tailwind 4 環境下 background 無法套用 |
| CSS `offset-path` + mask（**採用**） | 1 個 | **是**（compositor） | `linear-gradient` | 最佳效能 + 原生漸層 |
| Pseudo-element `transform: rotate` | 1 個 | 是 | 有限 | 角落速度不均勻 |

### 為何選擇 offset-path

- `offset-distance` 屬性可走 GPU compositor thread，與 `transform` 同等效能
- 單一 DOM 元素，無 SVG overhead
- 原生 `linear-gradient` 提供平滑漸層，不需多段模擬
- 瀏覽器支援：Chrome 46+、Firefox 72+、Safari 16+（覆蓋率 ~95.7%）

## 實作結構

### 三層架構

```
<div class="relative group">           ← 定位容器
  <div class="ws-border-beam-mask">    ← Mask 層：裁切為僅顯示邊框
    <div (beam element)>               ← Beam：沿路徑移動的漸層方塊
    </div>
  </div>
  <button>                             ← 實際的 workspace 按鈕
    <WorkspaceIcon />
  </button>
</div>
```

### 1. Mask 層（邊框裁切）

核心技巧：用兩層 opaque mask 搭配 `mask-composite: exclude`（XOR），只露出 border 區域。

```css
/* index.css */
.ws-border-beam-mask {
  mask: linear-gradient(#000 0 0) padding-box,
        linear-gradient(#000 0 0) border-box;
  -webkit-mask: linear-gradient(#000 0 0) padding-box,
               linear-gradient(#000 0 0) border-box;
  mask-composite: exclude;
  -webkit-mask-composite: xor;
}
```

**原理**：
- Layer 1：`padding-box` — 在內部區域（不含 border）塗滿 opaque
- Layer 2：`border-box` — 在整個元素（含 border）塗滿 opaque
- `exclude`/`xor`：兩層重疊的地方互相抵消 → 只剩 border 區域可見

Mask 層的 inline style：

```tsx
style={{
  inset: '-1px',                       // 比按鈕大 1px，讓邊框不貼著按鈕
  border: '1.5px solid transparent',   // 定義 border 寬度（= beam 線寬）
  visibility: showOrbit ? 'visible' : 'hidden',  // 不 unmount，讓動畫持續
}}
```

> **重要**：不可使用 `overflow: hidden`，否則 beam 會被裁切到 padding-box 而在 border 區域不可見。

### 2. Beam 元素（移動光點）

```tsx
<div style={{
  position: 'absolute',
  width: '36px',                       // 控制尾巴長度
  aspectRatio: '1',                    // 正方形，漸層向四方擴散
  offsetPath: 'rect(0 auto auto 0 round 36px)',  // 矩形路徑 + 圓角
  offsetAnchor: '90% 50%',            // 哪個點貼著路徑（90% = 略偏右中）
  background: `linear-gradient(to left, ${color}, ${color}44, transparent)`,
  animation: 'border-beam 3s linear infinite',
}} />
```

**參數說明**：

| 參數 | 值 | 作用 |
|------|-----|------|
| `width` | `36px` | Beam 正方形邊長，決定可見尾巴長度 |
| `offsetPath` | `rect(0 auto auto 0 round 36px)` | 定義矩形路徑，`round` 值控制路徑圓角 |
| `offsetAnchor` | `90% 50%` | Beam 的哪個點沿路徑走（偏右中 = 亮端貼路徑） |
| `background` | `linear-gradient(to left, 亮, 半透, 透明)` | 右亮左暗的漸層 |
| `animation` | `3s linear infinite` | 3 秒繞一圈 |

### 3. Keyframe

```css
@keyframes border-beam {
  100% { offset-distance: 100%; }
}
```

`offset-distance` 從 `0%`（路徑起點）到 `100%`（繞完一圈），瀏覽器自動沿 `offset-path` 定義的矩形路徑移動。

## 漸層方向與 offset-rotate

`offset-rotate` 預設值為 `auto`，beam 元素會自動旋轉使其正 x 軸對齊路徑切線方向。因此 `linear-gradient(to left, ...)` 的方向會隨路徑轉彎：

- 上邊（→）：亮端在右（前方），暗端在左（後方）✓
- 右邊（↓）：beam 旋轉 90°，亮端朝下（前方）✓
- 下邊（←）：beam 旋轉 180°，亮端朝左（前方）✓
- 左邊（↑）：beam 旋轉 270°，亮端朝上（前方）✓

## Active/Inactive 切換

```tsx
// 有狀態就 mount（動畫持續跑）
{aggregatedStatus && (
  <div style={{ visibility: showOrbit ? 'visible' : 'hidden' }}>
    ...
  </div>
)}
```

- **不用** `{showOrbit && ...}` 條件渲染 — 會導致 unmount/remount，動畫每次重頭開始
- **改用** `visibility: hidden` — 元素保持 mounted，CSS animation 在背景繼續跑
- 切回 inactive 時 beam 從當前位置繼續，不會跳回起點

## 狀態顏色對應

```typescript
const PILL_COLORS: Record<ActiveStatus, string> = {
  running: '#4ade80',  // 綠
  waiting: '#facc15',  // 黃
  error:   '#ef4444',  // 紅
}
```

## 涉及檔案

| 檔案 | 內容 |
|------|------|
| `spa/src/index.css` | `@keyframes border-beam` + `.ws-border-beam-mask` |
| `spa/src/features/workspace/components/ActivityBar.tsx` | `WorkspaceButton` 元件內的 beam 渲染邏輯 |

## 調整參數指南

| 想調什麼 | 改哪裡 |
|----------|--------|
| 線寬 | mask 層的 `border` 寬度 |
| 尾巴長度 | beam 的 `width`（越大尾巴越長） |
| 速度 | `animation` 的 duration（越小越快） |
| 漸層曲線 | `linear-gradient` 的色標位置 |
| 離按鈕距離 | mask 層的 `inset` 負值 |
| 路徑圓角 | `offsetPath` 的 `round` 值 |

## 參考資源

- [Magic UI — Border Beam](https://magicui.design/docs/components/border-beam)
- [MDN — offset-path](https://developer.mozilla.org/en-US/docs/Web/CSS/offset-path)
- [MDN — offset-distance](https://developer.mozilla.org/en-US/docs/Web/CSS/offset-distance)
- [MDN — mask-composite](https://developer.mozilla.org/en-US/docs/Web/CSS/mask-composite)
