user: Use the Agent tool (general-purpose) to count the lines of every .md file in this repo and report the total.

<tool Agent | Count lines of .md files>

agent: 已派出 subagent 去統計 repo 內所有 .md 檔的行數，目前還在執行。結果回來後，我會回報各檔行數與總計。

<subagent done | Agent "Count lines of .md files" finished>

agent: repo 內共有 3 個 `.md` 檔（已排除 `.git` 與 `node_modules`），總計 **12 行**：

| 檔案 | 行數 |
|---|---|
| `one.md` | 4 |
| `two.md` | 3 |
| `docs/three.md` | 5 |
| **總計** | **12** |

這是 subagent 回報的數字，我沒有另外複核。

<slash | /exit>
