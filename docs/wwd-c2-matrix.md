# WWD C2 真機矩陣

WWD-4 的驗收（plan `docs/plans/2026-10-18-wwd-plan.md`「Acceptance (C2)」）。依據 spec `docs/specs/2026-10-18-wwd-spec.md` 的 §4.2（狀態與轉移）、§4.3、§4.6、§5、§5.1、§11。

腳本是 `scripts/wwd-c2-matrix.sh`。**它還沒有在任何主機上跑過**：wwd 部署後第一次執行時，腳本裡標 `TODO(first-run)` 的地方是對路由名稱、CLI 輸出形狀的假設，第一次跑要逐條核對（見最後一節）。

## 1. 怎麼跑

從 mlab（origin）執行，需要 `ssh air26`、`ssh mserv`、`ssh air19` 都不用密碼可達。

```sh
sh scripts/wwd-c2-matrix.sh --list                 # 案例清單、需要的主機、預估時間、需要的掛鉤
sh scripts/wwd-c2-matrix.sh --dry-run --case 3     # 只印每一步，什麼都不執行（也不呼叫任何命令）
sh scripts/wwd-c2-matrix.sh --case 1,2,4           # 一個一個跑
sh scripts/wwd-c2-matrix.sh --all                  # 全部（約 58 分鐘）
```

| 旗標 | 意思 |
|---|---|
| `--dry-run` | 印出每一步，不執行 |
| `--manual` | 需要「主人回來」的案例（7）改成請人移動 air26 的滑鼠，不用掛鉤 |
| `--force` | 佇列裡已經有工作也照跑（預設要求佇列是空的） |
| `--keep` | 結束時留下暫存目錄（印出路徑），除錯用 |
| `C2_TIMEOUT_SCALE=0` | 環境變數，把所有等待縮成 0，只給腳本自己的 stub 測試用，真機不要設 |

### 每個案例的共同流程

1. **前置檢查**：四台都連得到；四台的 wwd 版本與 proto 相同；佇列是空的（除非 `--force`）；air19 的 worker 是關的。
2. 建一個暫存 repo（`$TMPDIR/wwd-c2.<pid>/repo`，一個 commit，內含 probe 任務，見 §5）。
3. 案例本身：用 `only_workers` 設定誰的 worker 開著，製造一個故障，等待，唯讀檢查，印 `ok`／`FAIL`。
4. 每個案例結束都多一項檢查：**air19 從這次執行開始後沒有 claim 任何工作**。
5. 結尾（也在 ^C、`exit`、SIGTERM 時）：恢復凍結的 wwd（`kill -CONT`）、把停掉的服務重新啟動、把改過的 worker 設定原樣 PUT 回去、刪掉建立的 fault 檔、取消它建的工作、刪除暫存與遠端暫存、刪掉四台的 `/tmp/wwd-c2-ran.log`。
   **會改變主機狀態的四項（解凍、重啟服務、還原 worker 設定、刪 fault 檔）做不到時，會列在結尾並讓結束碼是 1**（`FAIL: the clean-up could not put back: ...`），看到這行就要親自去那台主機看。
   取消工作、刪遠端暫存、刪 probe log 是 best-effort：失敗不會報也不影響結束碼；跑完後可用 §6 的指令順手確認。
6. 結束碼：有案例 FAIL 或清理失敗是 1，否則 0。SKIP（缺掛鉤）不算失敗。

### 腳本會做與不會做的事

故障注入**只用**：服務管理（`launchctl bootout／bootstrap／kickstart -k`、`systemctl stop／start／restart`）、對**精確 pid** 的 `kill -STOP／-CONT`（pid 來自服務管理，必須是大於 1 的數字，且 `ps` 確認是 `/wwd/bin/wwd`，否則整個案例中止）、自己的 worker 開關（`PUT /v1/worker`）。
**不用** pfctl、iptables、改 sudoers、改防火牆、`pkill`、`killall`。

腳本被 SIGKILL 時清理不會執行，見 §6。

## 2. 主機

| 主機 | 角色 | tailnet 位址 | 服務 |
|---|---|---|---|
| mlab | origin，也當 worker | 100.64.0.2 | launchd `gs.wake.wwd` |
| air26 | worker（主人會回來讓位） | 100.64.0.4 | launchd `gs.wake.wwd` |
| mserv | Linux worker（沙箱、測試 MySQL） | 100.64.0.6 | systemd `wwd.service` |
| air19 | worker **關閉**，整個矩陣不 claim | 100.64.0.1 | launchd `gs.wake.wwd` |

共同路徑：socket `~/.local/share/wwd/wwd.sock`、資料庫 `~/.local/share/wwd/wwd.db`、pool token `~/.config/wwd/pool.token`（只讀、不印）、TCP 埠 7871。

## 3. 數字（spec §4.2）

租約 60 秒、續約 15 秒；結果上傳重試最久 10 分鐘；infra release 之後，該 host 對**該 repo** 暫停 claim 5 分鐘（不是整台停止，它仍可 claim 其他 repo 的工作）；`queue_timeout` 預設 2 小時（probe 任務 `c2-expire` 設 20 秒）。

**`attempts` 的意思**：wwd.db 裡 `attempts` 是「已計入的失敗次數」（租約過期、infra 釋放算；reclaim 不算）。一次就成功是 0；手上這次 claim 的編號是 `attempts + 1`（`internal/wwd/queue/read.go`）。下表的 attempts 都是這個欄位。

## 4. 十四個案例

「檢查」欄的 `state／reason／host` 來自 `GET /v1/jobs/{id}`（`.state`、`.result.reason`、`.result.host`），`attempts` 查 wwd.db：`sqlite3 -readonly ~/.local/share/wwd/wwd.db "select attempts from jobs where id='…'"`。

| # | 情境 | 主機 | 約 | 掛鉤 |
|---|---|---|---|---|
| 1 | 兩個 claim 搶同一件工作 | mlab air26 | 3 分 | 無 |
| 2 | worker 跑到一半被殺：租約過期、第二次、failed | mlab air26 | 8 分 | 無 |
| 3 | 延遲的 heartbeat：舊 claim 被拒 | mlab air26 | 6 分 | 無 |
| 4 | origin 工作中途重啟 | mlab air26 | 4 分 | 無 |
| 5 | origin 連不上：取快照／執行中／上傳結果 | mlab air26 | 10 分 | H4（只 5a） |
| 6 | 結果上傳成功但 log 上傳失敗 | mlab air26 | 3 分 | H1 |
| 7 | air26 主人回來：reclaim | mlab air26 | 4 分 | H2，或 `--manual` |
| 8 | origin 壓力 2：它的工作先被取走 | mlab air26 mserv | 3 分 | H3 |
| 9 | 執行中的工作被同 key 的新工作取代 | mlab air26 | 6 分 | 無 |
| 10 | 排隊逾時 | mlab | 2 分 | 無 |
| 11 | pool token 錯 | 四台 | 1 分 | 無 |
| 12 | 未宣告的任務與不合法參數 | mlab air26 | 3 分 | 無 |
| 13 | mserv：PHP 工作用自己的資料庫、沙箱 | mlab mserv | 4 分 | 無 |
| 14 | air19 不 claim | mlab air19 | 1 分 | 無 |

> brief 的清單是 13 個情境加「air19 預設關」一條；腳本把最後一條獨立成案例 14，所以共 14 個。

### 案例 1：兩個 claim 搶同一件工作

- **前置**：只開 mlab、air26 的 worker。
- **做法**：(a) 真的：送 `c2-quick`，兩台自己搶。(b) 精確：關掉所有真 worker，腳本扮演 fakeA、fakeB，同時對 origin 的 TCP 發 `POST /v1/jobs/{id}/claim`（帶 pool token 與 worker 標籤 `os:darwin, arch:arm64`）。
- **預期**：(a) state `succeeded`；probe 檔 `/tmp/wwd-c2-ran.log` 兩台合計這個工作只有 **1** 行；attempts **0**。(b) 兩個回應碼排序後是 `200 409`；工作 state 是 `claimed`。

### 案例 2：worker 跑到一半被殺

- **前置**：只開 air26。送 `c2-sleep`（跑 90 秒）。
- **做法**：工作 `running` 後 `launchctl bootout` air26 的 wwd（第一次）；等租約過期；`bootstrap` 起來；等它再 `running`；再 bootout（第二次）。
- **預期**：第一次之後 state `queued`、attempts **1**；第二次之後 state `failed`、reason `lost`、attempts **2**；air26 上沒有環境變數含 `WWD_JOB=<id>` 的行程。
- **時間**：每次等租約最多 150 秒。

### 案例 3：延遲的 heartbeat

- **精確部分**（不開任何真 worker）：fakeA claim → 等 75 秒（過 60 秒租約）→ fakeB claim。
  預期：兩個 claim_id 不同；舊 claim 的 `heartbeat` 與 `result` 都回 **409**；工作仍是 `claimed`（新的那個）。
- **真實部分**：只開 air26，工作跑起來後 `kill -STOP` air26 的 wwd（過租約的 75 秒），同時開 mlab 的 worker；再 `kill -CONT`。
  預期：state `succeeded`、`.result.host` 是 **mlab**（不是解凍的 air26）、attempts **1**。

### 案例 4：origin 工作中途重啟

- 只開 air26，工作 `running` 後 `launchctl kickstart -k` mlab 的 wwd。
- 預期：state `succeeded`、host **air26**、attempts **0**（重啟寬限，租約沒算失敗）。

### 案例 5：origin 連不上

三個時點，用 `kill -STOP／-CONT` 凍結 mlab 的 wwd：

- **5a 取快照時**（需要 **H4**，否則 SKIP 5a）：worker 已 claim 但還沒開始跑時凍結 20 秒。預期 `succeeded`、attempts **1**（infra 釋放或租約過期一次）。
- **5b 執行中**：凍結 30 秒（短於租約）→ `succeeded`、attempts **0**；凍結 90 秒（長於租約）→ `succeeded`、attempts **1**。
- **5c 結果上傳時**：跑 84 秒時凍結 40 秒（工作約在 90 秒結束）→ `succeeded`、attempts **0**（上傳會重試）。

### 案例 6：結果上傳成功但 log 上傳失敗（需要 **H1**）

- origin 開 `fault.log_upload_fail`。預期：結果仍是 `succeeded`；`GET /v1/jobs/{id}/log` 回 **404**（`TODO(first-run)`：缺 log 的實際代碼）。

### 案例 7：air26 主人回來（需要 **H2**，或 `--manual`）

- 只開 air26，工作 `running` 後開 mlab 的 worker；在 air26 製造 owner active：用 H2 就是建立檔案 `~/.local/share/wwd/fault/owner-active`，`--manual` 就是人去動 air26 的滑鼠；等 air26 的 `GET /v1/facts` 出現 `owner.active == true`（最多 5 分鐘）。
- 預期：state `succeeded`、host **mlab**、attempts **0**（reclaim 不算）；air26 沒有 `WWD_JOB=<id>` 殘留行程。
- 不用 `--manual` 時，結尾會刪掉 H2 的檔（本來就存在的檔不會被刪）。

### 案例 8：origin 壓力 2（需要 **H3**）

- 只開 mserv 一個 worker；兩個 origin（mlab 與 air26）同時各送一個 `c2-any`；H3 讓 mlab 的 zone 是壓力 2。腳本把暫存 repo 用 tar 傳到 air26 當第二個 origin。
- 預期：兩個工作的 `.result.started_at` 比較，**mlab 這個先開始**（spec §4.6 origin_bonus）。
- 結尾會刪 air26 的暫存 repo。

### 案例 9：取代

- **可中斷**（`c2-sleep-int`）：A 跑起來後送 B（同 supersede key）。預期 A `superseded`、A 的 reason `superseded_by:<B>`；B 被取消。
- **不可中斷**（`c2-sleep-noint`）：A 跑到結束 → A `succeeded`，且 A 記著 `superseded_by` 是 B。

### 案例 10：排隊逾時

- 所有 worker 關，前景跑 `pdx test c2-expire`（`queue_timeout` 20 秒）。
- 預期：`pdx test` 結束碼 **6**；state `expired`；probe 檔沒有這個工作的行。

### 案例 11：pool token 錯

- 四台各 7 條路由（`/v1/info /v1/facts /v1/pool /v1/events /v1/jobs?state=queued /v1/worker /v1/jobs/x/snapshot`）：不帶 token 與帶錯 token 都回 **401**（共 56 項）；`POST /v1/jobs` 在 TCP 上不是路由，回 401／404／405 其一；同時有一個工作在跑，預期它照常 `succeeded`。
- token 不會被印出來。

### 案例 12：未宣告的任務與不合法參數

直接對 origin 的 socket `POST /v1/jobs` 四種不合法提交：未宣告的任務、`--bad-option`、`../../etc/hosts`、`a;touch /tmp/wwd-c2-pwned`。

- 預期：每個都是 state `failed`（`TODO(first-run)`：拒絕的 state／reason 字樣是 4e 定義，現在假設 `failed`；腳本只查 state，不查 reason）；probe 檔在 mlab、air26 都沒有它們的行；`/tmp/wwd-c2-pwned` 在 **mlab 與 air26 都**不存在；兩台的 `~/.local/share/wwd/work` 目錄數都沒變（沒有建工作區）。

### 案例 13：mserv 的 PHP 與沙箱

- 只開 mserv。(a) `c2-php-db`（需要 `db = "per-job"`）：PHP 腳本連上分到的資料庫、建表、寫、讀回，讀到 `round-trip` 才結束碼 0。預期 `succeeded`；結束後 mserv 上 `show databases` 沒有 `wwd_` 開頭的庫。
  (b) `c2-sandbox`：探針在沙箱內檢查，全部成立才結束碼 0：工作區（工作目錄）寫得進去（對照組，寫不進去就代表探針本身沒用）；`/run/docker.sock`、`/var/run/docker.sock` 都連不上（有 `curl` 時真的發 `/_ping` 請求、不得有回應；另外確認宿主上有 docker socket，否則這項拒絕什麼也沒證明。`InaccessiblePaths` 在原路徑留一個 mode 000 的 socket 替身，所以判斷的是「有沒有寫入權限」，不是存在與否）；`/home/wake`、`/etc`、`/usr/local`、`/usr/bin`、`/opt`、`/srv`、`/root`、`/var/lib`、`/var/log`、`/run` 都寫不進去（`ProtectSystem=strict`）；`sudo -n true` 不通；`/tmp` 可寫，且看不到案例事先放在宿主 `/tmp` 的 `wwd-c2-sentinel.<本次執行的標記>`。預期 `succeeded`；案例另外在宿主上確認工作寫的 `/tmp/wwd-c2-private.<標記>` 沒有出現在宿主的 `/tmp`（私有 `/tmp` 的雙向證明）。
  (c) `systemctl --failed` 沒有 `wwd-` 的 unit（工作的 transient unit 讀完 Result 就 `reset-failed`）。

### 案例 14：air19 不 claim

- 兩個獨立來源相加，總數必須是 **0**；另外 air19 的 worker 是關的。
  (1) wwd.db：`select id from jobs where claim_worker='air19'` 現在的 id 清單，扣掉**執行開始時**記下的清單（不看工作何時建立，所以矩陣開始前排隊、之後才被 air19 claim 的工作也會數到；清掉舊資料列也抵銷不了新增）。
  (2) air19 自己的 probe log（`/tmp/wwd-c2-ran.log`，開始時清空）：只要有任何一行，就表示 air19 真的跑過 probe，即使那個工作後來被別台接走、wwd.db 的 `claim_worker` 被覆寫也一樣。
  兩者都不能抓到的：air19 claim 後立刻被收回、而且沒有跑到 probe 的短暫 claim（wwd.db 的列被覆寫）；要完整排除需要 claim 事件紀錄，見 §8 第 6 項。

## 5. probe 任務

暫存 repo 的 `.wwd/tasks.toml` 宣告：`c2-quick`（3 秒）、`c2-any`（5 秒、沒有標籤限制）、`c2-sleep`（90 秒）、`c2-sleep-int`／`c2-sleep-noint`、`c2-expire`（queue_timeout 20 秒）、`c2-files`（吃 `{files}` 參數）、`c2-php-db`、`c2-sandbox`。darwin 任務標 `os:darwin`，Linux 任務標 `os:linux`。`probe.sh` 每次執行往 `/tmp/wwd-c2-ran.log` 寫一行 `job=… host=… start=…`（在工作區之外），案例就是靠數這個檔判斷「跑了幾次、在哪台」。

## 6. 腳本被 SIGKILL 或當掉之後（手動復原）

清理在 EXIT／INT／TERM 時會跑，但 `kill -9` 不會。逐台檢查：

```sh
# 1. 有沒有被凍結的 wwd（ps 的狀態欄是 T），四台都看：
for h in air26 mserv air19; do echo "== $h"; ssh $h "ps -eo pid,stat,command | grep '[w]wd/bin/wwd'"; done
ps -eo pid,stat,command | grep '[w]wd/bin/wwd'                 # mlab 本機
ssh <host> "kill -CONT <pid>"                                    # 只對這個確切 pid，沒有 pkill

# 2. 有沒有被停掉的服務：
launchctl list gs.wake.wwd                                        # mlab；air26／air19 前面加 ssh <host>
launchctl bootstrap gui/$(id -u) $HOME/Library/LaunchAgents/gs.wake.wwd.plist   # 沒在列表上才跑
ssh mserv 'systemctl is-active wwd.service || sudo systemctl start wwd.service'

# 3. worker 設定：四台各看一次。air19 必須是 enabled=false，其他台要回到矩陣之前的值
for h in air26 mserv air19; do echo "== $h"; ssh $h 'curl -sS --unix-socket $HOME/.local/share/wwd/wwd.sock http://wwd/v1/worker'; done
curl -sS --unix-socket ~/.local/share/wwd/wwd.sock http://wwd/v1/worker     # mlab

# 4. 殘留（四台）：fault 檔與 probe log
for h in air26 mserv air19; do ssh $h 'rm -f $HOME/.local/share/wwd/fault/owner-active /tmp/wwd-c2-ran.log'; done
rm -f ~/.local/share/wwd/fault/owner-active /tmp/wwd-c2-ran.log
ssh air26 'rm -rf ${TMPDIR:-/tmp}/wwd-c2.r.*'                   # 案例 8 傳去的暫存 repo
ls -d "${TMPDIR:-/tmp}"/wwd-c2.*                                 # mlab 的暫存
```

`$TMPDIR/wwd-c2.<pid>/saved/<host>` 存著該台**改動前**的 worker 設定（`GET /v1/worker` 的原文）。這個目錄只在清理沒跑完（被 SIGKILL）或用了 `--keep` 時才會留下；正常結束就已經刪了。要手動還原某一台：

```sh
# mlab：
curl -sS --unix-socket ~/.local/share/wwd/wwd.sock -X PUT -H 'Content-Type: application/json' -d @"$TMPDIR/wwd-c2.<pid>/saved/mlab" http://wwd/v1/worker
# 其他台（把檔案從 stdin 餵過去，不經命令列）：
ssh <host> "curl -sS --unix-socket \$HOME/.local/share/wwd/wwd.sock -X PUT -H 'Content-Type: application/json' -d @- http://wwd/v1/worker" < "$TMPDIR/wwd-c2.<pid>/saved/<host>"
```

## 7. 需要產品端掛鉤才能注入的情境（建議，交 lead 決定誰做）

目前沒有這些掛鉤，案例 5a、6、7（不用 `--manual`）、8 會被 **SKIP**（不算失敗）。建議的最小掛鉤，**全部預設關**，要在設定檔明確打開（`[fault] enabled = true`），打開時 wwd 啟動與每次觸發都要大聲寫警告 log；只在測試用的主機打開。

| 掛鉤 | 作用 | 案例 | 開在哪台（腳本查哪台的 `/v1/info`） |
|---|---|---|---|
| H0 | `GET /v1/info` 多一個 `fault` 物件，宣告開了哪些掛鉤。腳本靠它決定 SKIP | 5a、6、7、8（有用到掛鉤的案例） | 下面各掛鉤所在的台 |
| H1 `fault.log_upload_fail` | origin 拒收 log 上傳（結果上傳不受影響） | 6 | mlab（origin） |
| H2 `fault.owner_active_file` | 檔案 `~/.local/share/wwd/fault/owner-active` 存在時，facts 的 `owner` 視為 active | 7 | air26 |
| H3 `fault.zone` | 覆蓋 origin 的 derived zone（讓它是壓力 2） | 8 | mlab |
| H4 `fault.snapshot_delay_s` | worker 取快照前延遲 N 秒，讓 5a 的凍結時機是確定的 | 5a | air26（worker；延遲發生在它） |

案例 3 的真實部分不需要 heartbeat 延遲掛鉤：它用 `kill -STOP` 凍結整個 wwd 達到同樣效果。

不需要掛鉤的情境怎麼辦到：案例 1、3 的精確部分由腳本自己扮演 worker 對 origin 的 TCP 發 claim／heartbeat／result（所以那時所有真 worker 都先關）。

## 8. 第一次真機執行要核對的假設（`TODO(first-run)`）

1. `pdx test --detach --json <task>` 印的是 `{"id":"…"}`。
2. `GET /v1/jobs/{id}` 的 JSON 形狀（`.state`、`.result.reason`、`.result.host`、`.result.started_at`、`.superseded_by`）。
3. `PUT /v1/worker` 收的 body 形狀就是 `GET /v1/worker` 給的。
4. 缺 log 時 `GET /v1/jobs/{id}/log` 回 404。
5. 案例 12：被拒絕的遠端 task 最終的 state／reason 字樣（4e 定義）。
6. 案例 14 與每個案例結尾的 air19 檢查：wwd.db 的 `jobs.claim_worker` 在 claim 後是否記著 worker 名、reclaim 是否覆寫它；有沒有 claim 事件表或事件序號，有的話改用它（單調、不會被清理或覆寫抵銷）。
7. 案例 13：per-job 資料庫的環境變數名（暫用 `DB_DATABASE`／`DB_HOST`／`DB_PORT`／`DB_USERNAME`／`DB_PASSWORD`）、mserv 測試 MySQL 的資料庫命名與登入方式。
8. air26 非互動 shell 的 PATH 找得到 `pdx`（案例 8）。

## 9. 測試

- `scripts/wwd-c2-matrix_test.sh`：`--dry-run` 的輸出斷言（每個案例的關鍵步驟與預期值），並用間諜指令證明 dry-run 不呼叫任何命令。
- `scripts/wwd-c2-matrix_stub_test.sh`：用假的 ssh／curl／pdx／sqlite3／launchctl 真跑非 dry-run 的路徑（含清理、清理失敗、pid 檢查、既有檔不刪）。
- 兩個都用 `sh <檔>` 跑，最後一行印 `ok`。
