# P3 — Link detection 設定遷移

> 對應 SPEC.md `# P3` 段。本檔吸收 PLAN 第二輪 codex review 與 P3 相關修訂。

## v4 修訂指引（實作前必看）

| Task | 修訂 | 來源 |
|---|---|---|
| **3.1** | i18n 路徑改 `spa/src/locales/*.json`（**不是 `spa/src/i18n/`**），跑 `spa/src/locales/locale-completeness.test.ts` 驗證 | 通用 review D2 |
| **3.2** | `EditorLinkDetectionSection` 放 `spa/src/components/settings/editor/EditorLinkDetectionSection.tsx`（子目錄），不直接堆 `settings/` 根 | 體質 review #13 |
| **3.3** | wire 進 P1 拆出來的 `register-modules/editor-module.tsx` 的 `settings` 陣列（不是原 `register-modules.tsx`） | B 決議副作用 |
| **All** | commit message lowercase | 通用 review C2 |
| **Verification** | 加一個 Phase 3 verification + PR task（與 P1/P2/P4 格式對齊）：跑 vitest + lint + build + go test，PR 描述引用 `SPEC.md (rev 6, P3)`，兩輪 codex review | 通用 review C1 |

---

PR 結束標準：Editor purdex settings 有 3 個 file path 偵測開關；Terminal settings 只剩 bare；停用 Editor 後 3 個開關不見。

## Task 3.1 — i18n key 遷移

**Files:**
- Modify: `spa/src/locales/zh-TW.json`, `spa/src/locales/en.json`

- [ ] **Step 1: 移動 key**

把 zh-TW.json + en.json 的 `settings.terminal.link_detect.absolute.*` / `tilde.*` / `relative_slash.*` 三組 key **複製** 到 `settings.editor.link_detect.absolute.*` / 等對應位置。原 `settings.terminal.link_detect.bare.*` 保留。

刪除 `settings.terminal.link_detect.absolute.*` / `tilde.*` / `relative_slash.*`（alpha 階段不需 backward compat）。

- [ ] **Step 2: Commit**

```bash
git add spa/src/locales/zh-TW.json spa/src/locales/en.json
git commit -m "i18n: move file-path link detect keys to editor scope"
```

---

## Task 3.2 — `LinkDetectionSection` 縮減 + `EditorLinkDetectionSection` 新建

**Files:**
- Modify: `spa/src/components/settings/LinkDetectionSection.tsx`
- Create: `spa/src/components/settings/editor/EditorLinkDetectionSection.tsx`
- Test: 兩個對應 .test.tsx

- [ ] **Step 1: Write failing tests**

新建 `spa/src/components/settings/editor/EditorLinkDetectionSection.test.tsx`：

```tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { EditorLinkDetectionSection } from './EditorLinkDetectionSection'

describe('EditorLinkDetectionSection', () => {
  it('renders absolute / tilde / relative_slash toggles', () => {
    render(<EditorLinkDetectionSection />)
    expect(screen.getByText(/absolute/i)).toBeInTheDocument()
    expect(screen.getByText(/tilde/i)).toBeInTheDocument()
    expect(screen.getByText(/relative/i)).toBeInTheDocument()
  })
})
```

縮減 `LinkDetectionSection.test.tsx` 既有測試只驗證 bare 開關。

- [ ] **Step 2: Run test, expect FAIL**

`EditorLinkDetectionSection` 尚未存在。

- [ ] **Step 3: Implement EditorLinkDetectionSection**

新建檔案：

```tsx
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { SettingItem } from '../SettingItem'
import { ToggleSwitch } from '../ToggleSwitch'

export function EditorLinkDetectionSection() {
  const linkDetectAbsolute = useUISettingsStore((s) => s.linkDetectAbsolute)
  const setLinkDetectAbsolute = useUISettingsStore((s) => s.setLinkDetectAbsolute)
  const linkDetectTilde = useUISettingsStore((s) => s.linkDetectTilde)
  const setLinkDetectTilde = useUISettingsStore((s) => s.setLinkDetectTilde)
  const linkDetectRelativeSlash = useUISettingsStore((s) => s.linkDetectRelativeSlash)
  const setLinkDetectRelativeSlash = useUISettingsStore((s) => s.setLinkDetectRelativeSlash)
  const t = useI18nStore((s) => s.t)

  return (
    <div>
      <h3 className="text-sm text-text-primary mt-6 mb-1">{t('settings.editor.link_detect.title')}</h3>
      <p className="text-xs text-text-secondary mb-3">{t('settings.editor.link_detect.desc')}</p>

      <SettingItem label={t('settings.editor.link_detect.absolute.label')} description={t('settings.editor.link_detect.absolute.desc')}>
        <ToggleSwitch label={t('settings.editor.link_detect.absolute.label')} checked={linkDetectAbsolute} onChange={setLinkDetectAbsolute} />
      </SettingItem>

      <SettingItem label={t('settings.editor.link_detect.tilde.label')} description={t('settings.editor.link_detect.tilde.desc')}>
        <ToggleSwitch label={t('settings.editor.link_detect.tilde.label')} checked={linkDetectTilde} onChange={setLinkDetectTilde} />
      </SettingItem>

      <SettingItem label={t('settings.editor.link_detect.relative_slash.label')} description={t('settings.editor.link_detect.relative_slash.desc')}>
        <ToggleSwitch label={t('settings.editor.link_detect.relative_slash.label')} checked={linkDetectRelativeSlash} onChange={setLinkDetectRelativeSlash} />
      </SettingItem>
    </div>
  )
}
```

- [ ] **Step 4: 縮減 LinkDetectionSection**

替換 `spa/src/components/settings/LinkDetectionSection.tsx` 內容：

```tsx
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { SettingItem } from './SettingItem'
import { ToggleSwitch } from './ToggleSwitch'

export function LinkDetectionSection() {
  const linkDetectBareFilename = useUISettingsStore((s) => s.linkDetectBareFilename)
  const setLinkDetectBareFilename = useUISettingsStore((s) => s.setLinkDetectBareFilename)
  const t = useI18nStore((s) => s.t)

  return (
    <div>
      <h3 className="text-sm text-text-primary mt-6 mb-1">{t('settings.terminal.link_detect.title')}</h3>
      <p className="text-xs text-text-secondary mb-3">{t('settings.terminal.link_detect.desc')}</p>

      <SettingItem label={t('settings.terminal.link_detect.bare.label')} description={t('settings.terminal.link_detect.bare.desc')}>
        <ToggleSwitch label={t('settings.terminal.link_detect.bare.label')} checked={linkDetectBareFilename} onChange={setLinkDetectBareFilename} />
      </SettingItem>
    </div>
  )
}
```

- [ ] **Step 5: Run tests, expect PASS**

```
cd spa && npx vitest run src/components/settings/
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/settings/editor/EditorLinkDetectionSection.tsx spa/src/components/settings/LinkDetectionSection.tsx spa/src/components/settings/editor/EditorLinkDetectionSection.test.tsx spa/src/components/settings/LinkDetectionSection.test.tsx
git commit -m "feat(spa): split link detection between terminal and editor sections"
```

---

## Task 3.3 — Wire `EditorLinkDetectionSection` into editor module（在 `register-modules/editor-module.tsx`）

**Files:**
- Modify: `spa/src/lib/register-modules/editor-module.tsx`（**不再修改 `register-modules.tsx`** 過渡 shim）

- [ ] **Step 1: Append to editor module settings array**

在 `editorModuleDefinition.settings: [...]` 陣列裡加：

```tsx
{
  localId: 'link-detect',
  scope: 'purdex',
  order: 8,
  labelKey: 'settings.editor.link_detect.title',
  component: EditorLinkDetectionSection,
},
```

並在 `editor-module.tsx` 頂部 import：

```tsx
import { EditorLinkDetectionSection } from '../../components/settings/editor/EditorLinkDetectionSection'
```

- [ ] **Step 2: Run integration test**

```
cd spa && npx vitest run
```

預期全綠。

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/register-modules/editor-module.tsx
git commit -m "feat(spa): wire editor link detection section into editor module"
```

---

## Task 3.4 — Phase 3 verification + PR

- [ ] **Step 1: Full test + lint + build + go test**

```bash
cd spa && pnpm install && npx vitest run && pnpm run lint && pnpm run build
go test ./...
```

Expected：全綠。

- [ ] **Step 2: 開 PR**

```bash
git push -u origin worktree-worktree-editor-self-contained
gh pr create --title "refactor(spa): migrate file path link detection settings to editor" --body "$(cat <<'EOF'
## Summary

- 拆 LinkDetectionSection 為兩個元件（Terminal section 只剩 bare；Editor purdex section 有三個 file path 開關）
- i18n key 從 settings.terminal.link_detect.{absolute,tilde,relative_slash}.* 移到 settings.editor.link_detect.* （路徑 spa/src/locales/）
- EditorLinkDetectionSection 放 components/settings/editor/ 子目錄
- wire 進 register-modules/editor-module.tsx 的 settings 陣列

## Test plan

- [ ] cd spa && pnpm install && npx vitest run && pnpm run lint && pnpm run build
- [ ] go test ./...
- [ ] 手動：Editor 啟用 → Settings → Editor → 三個 file path 開關出現
- [ ] 手動：Editor 停用 + 重載 → 三個 file path 開關消失
- [ ] 手動：Terminal Settings 只剩 bare filename 開關

Spec: SPEC.md (rev 6, P3)
EOF
)"
```

- [ ] **Step 3: 委派 codex 兩輪 review**

```bash
node "${CLAUDE_PLUGIN_ROOT:-$HOME/.claude/plugins/cache/openai-codex/codex/1.0.2}/scripts/codex-companion.mjs" review --background
node "${CLAUDE_PLUGIN_ROOT:-$HOME/.claude/plugins/cache/openai-codex/codex/1.0.2}/scripts/codex-companion.mjs" adversarial-review --background "P3 link detection migration"
```

依「Review 問題彙整」表格規則處理 finding，merge 後進 P4。

---

