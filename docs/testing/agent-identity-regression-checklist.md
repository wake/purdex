# Agent Identity / Liveness Regression Checklist

> 日期：2026-04-20
> 來源：
> - `docs/specs/2026-04-20-agent-identity-and-liveness-convergence-design.md` §9
> - `docs/superpowers/plans/2026-04-20-agent-identity-and-liveness-convergence.md` Phase 0

## 手動驗證情境

- [ ] 1. tmux 內啟動 CC
  預期：icon 穩定為 `cc`
- [ ] 2. 從 CC 觸發 detached Codex review
  預期：`cc` icon 不被覆蓋；Codex 事件不會寫進 CC session，reject reason=`pid_not_in_pane_tree`
- [ ] 3. 新開純 Codex tmux session
  預期：icon 切為 `codex`
- [ ] 4. CC 內呼叫 Codex 作為 subprocess（非 detached）
  預期：CC + Codex 兩個 frame 共存；經過至少一輪 quiet period / sweep 後仍不誤清；Codex 結束後回 CC
- [ ] 5. CC `argv[0]=2.1.114` 情況下送 hook
  預期：verify 通過，Identify 仍命中 `cc`（使用 ExePath basename）
- [ ] 6. 從 node wrapper 啟動 CC / Codex（例如 `node /path/to/<agent>/cli.js`）
  預期：verify 通過，Identify 命中對應 agent（使用 argv pattern）
- [ ] 7. Codex 內 `apply_patch` 長時間編輯（無 `PreToolUse` hook）
  預期：icon 維持 `running` 或轉 `idle`；畫面靜止時可進入 idle
- [ ] 8. Codex `Ctrl+C` 退回 shell
  預期：icon 在 1.5s idle 偵測 + 下一輪 sweep 視窗內回到 terminal（最慢約 3.5s）
- [ ] 9. CC 觸發 Notification，使用者直接在 terminal 回應
  預期：icon 從 `waiting` 回到 `running`
- [ ] 10. `kill -9` 殺掉 CC 程序
  預期：icon 在下個 sweep 內（<=2s）回到 terminal
- [ ] 11. Daemon restart 時有 3 個活躍 agent session
  預期：replay 後三個 icon 正確恢復
- [ ] 12. Daemon restart 後原 PID 被 OS 重用為不同程序
  預期：replay 時該 frame 被丟棄（`start_time` 不符）
- [ ] 13. tmux session rename（`tmux rename-session`）或 tmux 短暫失聯 / pane 暫時無法解析
  預期：不因 tmux 名稱或 tmux 短暫失聯而大屠殺清除；rename 時 icon 不消失、不閃爍
- [ ] 14. 全新安裝且 CC 已在某 tmux session 跑著
  預期：icon 維持 terminal，直到下一個 hook 才切換；此為可接受行為（spec §3.2）
