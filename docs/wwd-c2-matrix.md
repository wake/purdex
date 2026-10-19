# WWD C2 真機矩陣

WWD-4 的驗收（plan `docs/plans/2026-10-18-wwd-plan.md`「Acceptance (C2)」）。依據 spec `docs/specs/2026-10-18-wwd-spec.md` 的 §4.2（狀態與轉移）、§4.3、§4.6、§5、§5.1、§11。

腳本是 `scripts/wwd-c2-matrix.sh`。**它還沒有在任何主機上跑過。** 腳本的路由、回應欄位、狀態名稱、reason 字樣、結束碼、worker 設定的 PUT 格式、fault 設定鍵與各格預期的 attempts，已在 2026-10-19 對照 main（`806591dcb`）的程式碼逐項核對並修正；程式碼讀不出來的（主機環境）只剩 §8 的少數幾項，第一次真機執行時留意。

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
| `C2_RAN_LOG` | 環境變數，換掉 probe 記錄檔的路徑（預設 `/tmp/wwd-c2-ran.log`）；腳本的 stub 測試用它，免得碰到正在跑的矩陣的檔案。值是字面字串，由各主機的 shell 展開（例 `'$HOME/wwd-c2-ran.log'`）；只接受字母、數字與 `_ . / -`（可加字面 `$HOME`），其他字元腳本以結束碼 2 拒絕 |
| `C2_TIMEOUT_SCALE=0` | 環境變數，把所有等待縮成 0，只給腳本自己的 stub 測試用，真機不要設 |

### 每個案例的共同流程

連線：對 air26／mserv／air19 的 ssh 逾時 20 秒，連不上（結束碼 255）會等 2 秒再試一次；還是連不上，**這一格 FAIL**（印 `FAIL ssh to <host> failed twice`，不是案例結果），腳本照常往下清理並印總結，不會中途結束。案例內的指令失敗不會讓整支腳本停下來（該案例的檢查會 FAIL）；清理由 `trap` 保證在任何結束路徑都跑。

1. **前置檢查**：四台都連得到；四台的 wwd 版本與 proto 相同；佇列是空的（除非 `--force`）；air19 的 worker 是關的；要跑案例 8 時，在 air26 找得到 `pdx`（依序找非互動 shell 的 PATH、`~/.local/bin/pdx`、`~/.config/pdx/bin/pdx`，印出用的是哪一個）。
2. 建一個暫存 repo（`$TMPDIR/wwd-c2.<pid>/repo`，一個 commit，內含 probe 任務，見 §5）。
3. 要用 air26 的 worker 的案例（不含 8，air26 在那裡只是第二個 origin；不含 11，只送 token）開始前，先等 air26 的 `facts.owner.active` 不是 true，最多 10 分鐘（印 `waiting for air26's owner to be idle`）；等不到這個案例 SKIP 並寫原因，不算 FAIL。
   案例本身：用 `only_workers` 設定誰的 worker 開著，製造一個故障，等待，唯讀檢查，印 `ok`／`FAIL`。「等到 running／claimed」是硬前置：等不到就 `FAIL precondition not reached`（附該工作與 air26 owner 的狀態），這個案例剩下的檢查不做，不會出現建立在沒發生的事情上的 `ok`。
4. 每個案例結束都多一項檢查：**air19 從這次執行開始後沒有 claim 任何工作**。
5. 結尾（也在 ^C、`exit`、SIGTERM 時）：恢復凍結的 wwd（`kill -CONT`）、把改過的 worker 設定原樣 PUT 回去、刪掉建立的 fault 檔、取消它建的工作、刪除暫存與遠端暫存、刪掉四台的 `/tmp/wwd-c2-ran.log`。
   **會改變主機狀態的三項（解凍、還原 worker 設定、刪 fault 檔）做不到時，會列在結尾並讓結束碼是 1**（`FAIL: the clean-up could not put back: ...`），看到這行就要親自去那台主機看。
   取消工作、刪遠端暫存、刪 probe log 是 best-effort：失敗不會報也不影響結束碼；跑完後可用 §6 的指令順手確認。
6. 結束碼：有案例 FAIL 或清理失敗是 1，否則 0。SKIP（缺掛鉤）不算失敗。

### 腳本會做與不會做的事

故障注入**只用**：重啟 origin（案例 4：`launchctl kickstart -k`、`systemctl restart`）、對**精確 pid** 的 `kill -STOP／-CONT`（pid 來自服務管理，必須是大於 1 的數字，且 `ps` 確認是 `/wwd/bin/wwd`，否則整個案例中止）、自己的 worker 開關（`PUT /v1/worker`）。
**不用** pfctl、iptables、改 sudoers、改防火牆、`pkill`、`killall`，也**不停止任何 wwd**：`launchctl bootout`、`systemctl stop` 送的是 SIGTERM，wwd 會優雅結束，把手上的工作以 `reclaim` 歸還（不計 attempts），租約不會過期；要讓租約過期，只能讓 wwd 無法通話，也就是凍結它（`kill -STOP`）。案例 2、3、5 因此都用凍結。

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

**`attempts` 的意思**：wwd.db 裡 `attempts` 是「已計入的失敗次數」（租約過期、infra 釋放算；reclaim 不算，wwd 被 SIGTERM 停止時的歸還就是 reclaim）。一次就成功是 0；手上這次 claim 的編號是 `attempts + 1`（`internal/wwd/queue/read.go`）。同一張表還有 `lapses`（租約過期的次數）：**第二次過期就以 `failed`／`lost` 結束**（`maxLapses = 2`，`internal/wwd/queue/claim.go`）；infra 釋放計 attempts 但不計 lapses。下表的 attempts 都是 attempts 欄位。

## 4. 十四個案例

「檢查」欄的 `state／reason／host` 來自 `GET /v1/jobs/{id}`（`.state`、`.result.reason`、`.result.host`），`attempts` 查 wwd.db：`sqlite3 -readonly ~/.local/share/wwd/wwd.db "select attempts from jobs where id='…'"`。

| # | 情境 | 主機 | 約 | 掛鉤 |
|---|---|---|---|---|
| 1 | 兩個 claim 搶同一件工作 | mlab air26 | 3 分 | 無 |
| 2 | worker 跑到一半聯絡不上（wwd 凍結）：租約過期、第二次、failed | mlab air26 | 8 分 | 無 |
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

### 案例 2：worker 跑到一半聯絡不上

- **前置**：只開 air26。送 `c2-sleep`（跑 90 秒）。
- **做法**：工作 `running` 後凍結 air26 的 wwd（`kill -STOP`，精確 pid）超過租約（第一次）；等 origin 把工作收回；解凍（它的 worker 發現租約沒了，結束命令，又 claim 同一個工作）；等它再 `running`；再凍結（第二次）。
  **不用 `launchctl bootout`／`systemctl stop`**：那是 SIGTERM，wwd 會以 `reclaim` 歸還工作，attempts 不增加，也永遠不會走到 `failed`（見 §3）。
- **預期**：第一次之後 state `queued`、attempts **1**；第二次之後 state `failed`、reason `lost`、attempts **2**（第二次租約過期就是 `maxLapses`）；解凍後 air26 上沒有環境變數含 `WWD_JOB=<id>` 的行程。
- **時間**：每次等租約最多 150 秒。mlab 的 worker 在這個案例是關的，所以凍結期間工作停在 `queued`，不會被別台接走。

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

- **5a 取快照時**（需要 **H4**，否則 SKIP 5a）：worker 已 claim 但還沒開始跑時凍結 20 秒。H4 讓 air26 的 worker 在向 mlab 取快照的請求前等 N 秒（`fault.snapshot_delay_s`，**建議 5～10**，請求才會落在凍結窗口裡）；取快照期間工作停在 `claimed`。預期 `succeeded`、attempts **0**：快照 client 沒有整體逾時，請求在凍結期間卡住、解凍後照常完成；20 秒短於 60 秒租約，租約沒有過期，也沒有 infra 釋放。（要測「租約在取快照時過期」，凍結要超過 75 秒，那時是 attempts 1。）
- **5b 執行中**：凍結 30 秒（短於租約）→ `succeeded`、attempts **0**；凍結 90 秒（長於租約）→ `succeeded`、attempts **1**。
- **5c 結果上傳時**：跑 84 秒時凍結 30 秒（工作約在 90 秒結束）→ `succeeded`、attempts **0**（上傳會重試）。凍結不能太長：worker 的期限是最後一次成功 heartbeat（每 15 秒一次）起 60 秒，凍結 40 秒時餘裕只有約 7～11 秒，主機一忙就會多算一次。

### 案例 6：結果上傳成功但 log 上傳失敗（需要 **H1**）

- origin 開 `fault.log_upload_fail`。預期：結果仍是 `succeeded`；`GET /v1/jobs/{id}/log` 回 **404**（沒有 log 這一列就是 not found，`internal/wwd/queue/log.go`、`jobsapi/api.go`）。H1 只攔 TCP 上的 log 上傳，所以是 air26 這個遠端 worker 被攔；mlab 自己的 worker 走本機路徑不受影響。

### 案例 7：air26 主人回來（需要 **H2**，或 `--manual`）

- 只開 air26，工作 `running` 後開 mlab 的 worker；在 air26 製造 owner active：用 H2 就是建立檔案 `~/.local/share/wwd/fault/owner-active`，`--manual` 就是人去動 air26 的滑鼠；等 air26 的 `GET /v1/facts` 出現 `owner.active == true`（最多 5 分鐘）。
- 預期：state `succeeded`、host **mlab**、attempts **0**（reclaim 不算）；air26 沒有 `WWD_JOB=<id>` 殘留行程。
- 不用 `--manual` 時，結尾會刪掉 H2 的檔（本來就存在的檔不會被刪）。

### 案例 8：origin 壓力 2（需要 **H3**）

- 只開 mserv 一個 worker；兩個 origin（mlab 與 air26）同時各送一個 `c2-any`；H3 讓 mlab 的 zone 是壓力 2。腳本把暫存 repo 用 tar 傳到 air26 當第二個 origin。
- 預期：兩個工作的 `.result.started_at` 比較，**mlab 這個先開始**（spec §4.6 origin_bonus）。
- 結尾會刪 air26 的暫存 repo。

### 案例 9：取代

- **可中斷**（`c2-sleep-int`）：A 跑起來後送 B（同 supersede key）。預期 A 的 state 是 **`cancelled`**（不是 `superseded`：origin 只標 `cancel_requested`，worker 對取消只會回報 `cancelled`），A 的 `result.reason` 是 `superseded_by:<B>`、`result.superseded_by` 是 B；B 被取消。`superseded` 是「還在排隊時被取代」的 state（spec §4.2 的 As built 段）。
- **不可中斷**（`c2-sleep-noint`）：A 跑到結束 → A `succeeded`，且 A 記著 `superseded_by` 是 B。

### 案例 10：排隊逾時

- 所有 worker 關，前景跑 `pdx test c2-expire`（`queue_timeout` 20 秒）。
- 預期：`pdx test` 結束碼 **6**；state `expired`；probe 檔沒有這個工作的行。

### 案例 11：pool token 錯

- 四台各 7 條路由（`/v1/info /v1/facts /v1/pool /v1/events /v1/jobs?state=queued /v1/worker /v1/jobs/x/snapshot`）：不帶 token 與帶錯 token 都回 **401**（共 56 項）；`POST /v1/jobs` 在 TCP 上不是路由，回 401／404／405 其一；同時有一個工作在跑，預期它照常 `succeeded`。
- token 不會被印出來。

### 案例 12：未宣告的任務與不合法參數

直接對 origin 的 socket `POST /v1/jobs` 四種不合法提交：未宣告的任務、`--bad-option`、`../../etc/hosts`、`a;touch /tmp/wwd-c2-pwned`。

- 預期：origin **照單全收**（提交時只驗格式，不重讀 manifest，回 `200 {"id":…}`）；拒絕發生在 worker 的 Prepare：每個都是 state `failed`、沒有 exit、**`result.reason` 是空字串**，理由是 `result.summary.tail` 的唯一一行，冒號前是代碼：未宣告的任務是 `task_unknown`，其餘三個（`--bad-option`、`../../etc/hosts`、`a;touch …`）是 `args_invalid`（`internal/wwd/worker/remotejob/remotejob.go`）。腳本檢查 state 與代碼；origin 沒有回 id 的話立刻 FAIL，不去等一個不存在的工作。probe 檔在 mlab、air26 都沒有它們的行；`/tmp/wwd-c2-pwned` 在 **mlab 與 air26 都**不存在；兩台的 `~/.local/share/wwd/work` 目錄數與案例前相同（worker 先取快照才驗證，所以會建工作區，但清理後沒有殘留）。

### 案例 13：mserv 的 PHP 與沙箱

- 只開 mserv。(a) `c2-php-db`（需要 `db = "per-job"`）：PHP 腳本連上分到的資料庫、建表、寫、讀回，讀到 `round-trip` 才結束碼 0。預期 `succeeded`（`db_probe.php` 只有在連上分到的資料庫、建表、寫入、讀回 `round-trip` 之後才結束碼 0，所以 `succeeded` 就證明庫存在過）；結果送出之後 wwd 才移除工作區與資料庫（`Cleanup`，Drop 最多等 30 秒），所以腳本每 3 秒問一次，**最多 45 秒內**這個案例**自己的**那個庫要消失。庫名由 job id 算出，和 jobdb 一樣：`wwd_` 加 SHA-256(job id) 的前 32 位十六進位；**不從 job 的 log 讀**：矩陣期間 mlab 開著 H1（`log_upload_fail`），mserv 的 worker 對 mlab 的 log 上傳都被拒，那份 log 不在。只問這一個庫，不數整個實例：別的 job 的庫（`--force`、殘留）不是這個案例要判斷的。查法：在 mserv 用 jobdb 自己的 client 檔 `~/.config/wwd/mysql-admin.cnf`（帳號 `wwd_admin`，連專用的 dev 實例）呼叫 `CALL wwdctl.wwd_list_dbs()`；預設的 `~/.my.cnf` 是另一個實例，看不到這些庫，答案永遠是 0，所以不能用。連不上時答案是 `?`，不是 0，案例會 FAIL。
  (b) `c2-sandbox`：探針在沙箱內檢查，全部成立才結束碼 0：工作區（工作目錄）寫得進去（對照組，寫不進去就代表探針本身沒用）；`/run/docker.sock`、`/var/run/docker.sock` 都連不上（有 `curl` 時真的發 `/_ping` 請求、不得有回應；另外確認宿主上有 docker socket，並在沙箱外用矩陣登入的帳號（mserv 上就是工作帳號，在 docker 群組）發 `/_ping` 要得到 `OK`（對照組：沒有沙箱時是通的，沙箱內才不通；假設矩陣登入帳號＝工作帳號）。`InaccessiblePaths` 在原路徑留一個 mode 000 的 socket 替身，所以判斷的是「有沒有寫入權限」，不是存在與否）；`/home/wake`、`/etc`、`/usr/local`、`/usr/bin`、`/opt`、`/srv`、`/root`、`/var/lib`、`/var/log`、`/run` 都寫不進去（`ProtectSystem=strict`）；`sudo -n true` 不通；`/tmp` 可寫，且看不到案例事先放在宿主 `/tmp` 的 `wwd-c2-sentinel.<本次執行的標記>`。預期 `succeeded`；案例另外在宿主上確認工作寫的 `/tmp/wwd-c2-private.<標記>` 沒有出現在宿主的 `/tmp`（私有 `/tmp` 的雙向證明）。
  (c) `systemctl --failed` 沒有 `wwd-` 的 unit（工作的 transient unit 讀完 Result 就 `reset-failed`）。

### 案例 14：air19 不 claim

- 兩個獨立來源相加，總數必須是 **0**；另外 air19 的 worker 是關的。
  (1) wwd.db：`select id from jobs where claim_worker='air19'` 現在的 id 清單，扣掉**執行開始時**記下的清單（不看工作何時建立，所以矩陣開始前排隊、之後才被 air19 claim 的工作也會數到；清掉舊資料列也抵銷不了新增）。
  (2) air19 自己的 probe log（`/tmp/wwd-c2-ran.log`，開始時清空）：只要有任何一行，就表示 air19 真的跑過 probe，即使那個工作後來被別台接走、wwd.db 的 `claim_worker` 被覆寫也一樣。
  兩者都不能抓到的：air19 claim 後立刻被收回、而且沒有跑到 probe 的短暫 claim（requeue 時 `claim_worker` 被清成 NULL）；要完整排除需要 claim 事件紀錄，目前沒有，見 §8 第 6 項。

## 5. probe 任務

暫存 repo 的 `.wwd/tasks.toml` 宣告：`c2-quick`（3 秒）、`c2-any`（5 秒、沒有標籤限制）、`c2-sleep`（90 秒）、`c2-sleep-int`／`c2-sleep-noint`、`c2-expire`（queue_timeout 20 秒）、`c2-files`（吃 `{files}` 參數）、`c2-php-db`、`c2-sandbox`。darwin 任務標 `os:darwin`，Linux 任務標 `os:linux`。`probe.sh` 每次執行往 `/tmp/wwd-c2-ran.log` 寫一行 `job=… host=… start=…`（在工作區之外），案例就是靠數這個檔判斷「跑了幾次、在哪台」。

## 6. 腳本被 SIGKILL 或當掉之後（手動復原）

清理在 EXIT／INT／TERM 時會跑，但 `kill -9` 不會。逐台檢查：

```sh
# 1. 有沒有被凍結的 wwd（ps 的狀態欄是 T），四台都看：
for h in air26 mserv air19; do echo "== $h"; ssh $h "ps -eo pid,stat,command | grep '[w]wd/bin/wwd'"; done
ps -eo pid,stat,command | grep '[w]wd/bin/wwd'                 # mlab 本機
ssh <host> "kill -CONT <pid>"                                    # 只對這個確切 pid，沒有 pkill

# 2. 服務有在跑嗎（腳本不停服務，只有案例 4 重啟 origin；這是保險）：
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

## 7. 掛鉤與設定

案例 5a、6、7（不用 `--manual`）、8 需要故障掛鉤（`internal/wwd/fault`）：**全部預設關**，要在 `config.toml` 明確打開（`[fault] enabled = true` 加上要用的鍵）；打開時 wwd 啟動與每次觸發都寫警告 log；只在測試用的主機打開；設定檔改了要重啟 wwd 才生效。沒有開的話，那些案例會 **SKIP**（不算失敗）。

| 掛鉤 | 設定鍵 | 作用 | 案例 | 開在哪台（腳本查哪台的 `/v1/info`） |
|---|---|---|---|---|
| H0 | （無，自動） | `GET /v1/info` 多一個 `fault` 物件，宣告開了哪些掛鉤（沒開任何掛鉤時整個 `fault` 欄位不存在）。腳本靠它決定 SKIP | 5a、6、7、8 | 下面各掛鉤所在的台 |
| H1 | `log_upload_fail = true` | origin 拒收 log 上傳（TCP 上的 `PUT /v1/jobs/{id}/log` 回 503；結果上傳不受影響） | 6 | mlab（origin） |
| H2 | `owner_active_file = "<絕對路徑>"` | 該檔案存在時，facts 的 `owner` 視為 active。**值必須是字面的絕對路徑**：TOML 不展開 `~`、`$HOME`，`/v1/info` 也只回 `true`，不帶路徑。腳本建立與刪除的是 `$HOME/.local/share/wwd/fault/owner-active`，所以 air26 的設定要寫成它展開後的路徑（`/Users/wake/.local/share/wwd/fault/owner-active`）。worker 讓位看的是 facts 的 `owner.active`，與 `[owner]` 開不開無關（`yield.Observe`、`loop.Round`）；`[owner] enabled = false` 時 HID 不再被讀，只剩這個檔案能讓 owner 變 active，所以使用者在 air26 工作不會干擾其他案例（見下）。`[owner]` 關閉時 facts 原本沒有 owner 區塊（null），而 `yield.Observe` 對 null 不會結束讓位，所以掛鉤設了、檔案不在時 sampler 會補一個 `{active:false, reason:"fault: owner_active_file absent"}`（`daemon/sampler.go`），檔案刪掉 worker 才會回來；在這個修正之前，案例 7 之後 air26 的 worker 會一直讓位到重啟 | 7 | air26 |
| H3 | `zone = "warning"` | 覆蓋 origin 的 derived zone。值是 zone 名（`normal`／`warning`／`danger`），**不是數字**：壓力 2 就是 `warning`（origin_bonus +200，`danger` +400）。只對「worker 把它當 peer 讀」生效，案例 8 的 worker 是 mserv，成立 | 8 | mlab |
| H4 | `snapshot_delay_s = N`（0～3600） | worker 向 origin 取快照前等 N 秒，讓 5a 的凍結時機是確定的。**建議 5～10**（小於 5a 的 20 秒凍結窗口） | 5a | air26（worker；延遲發生在它） |

案例 3 的真實部分不需要 heartbeat 延遲掛鉤：它用 `kill -STOP` 凍結整個 wwd 達到同樣效果。

不需要掛鉤的情境怎麼辦到：案例 1、3 的精確部分由腳本自己扮演 worker 對 origin 的 TCP 發 claim／heartbeat／result（所以那時所有真 worker 都先關）。

### 7.1 矩陣之前與之後的設定

設定檔是各台的 `~/.config/wwd/config.toml`。部署（753）時四台都**沒有** `[fault]`、四台的 worker 都是關的；矩陣腳本會逐案例用 `PUT /v1/worker` 開關 worker，結束時照原樣放回去，所以 worker 不用手動處理。要手動做的只有 fault 設定與（mserv 的）沙箱與資料庫設定。

**跑之前**（每台先 `cp ~/.config/wwd/config.toml ~/.config/wwd/config.toml.pre-c2`，改完重啟 wwd）：

| 主機 | 加什麼 | 為什麼 |
|---|---|---|
| mlab | `[fault]`<br>`enabled = true`<br>`zone = "warning"`<br>**`log_upload_fail = true` 只在跑案例 6 時加**（單獨 `--case 6`，跑完移除並重啟） | H3（案例 8）、H1（案例 6）。**H1 不能在其他案例開著**：worker 在結果送出後還要重試 log 上傳最多 2 分鐘，這段時間仍占著 heavy 名額（#3404），下一格就在 90 秒內 claim 不到（2026-10-19 重跑時案例 3、4 因此失敗）。腳本的 precheck 在 mlab 的 `/v1/info` 顯示 `log_upload_fail` 開著、又選了 6 以外的案例時直接 FAIL。mlab 作為 origin 的工作在 `zone = "warning"` 下多 +200 分（只影響各 worker 偏好哪個 origin） |
| air26 | `[owner]`<br>`enabled = true`（正式設定本來就有，確認即可）<br>`[fault]`<br>`enabled = true`<br>`owner_active_file = "/Users/wake/.local/share/wwd/fault/owner-active"`<br>`snapshot_delay_s = 8` | H2（案例 7）、H4（案例 5a）。建議再加 `[owner] enabled = false`（跑完照樣由 `config.toml.pre-c2` 還原）：`[owner]` 開著時，使用者最近 5 分鐘有動 air26，worker 就不接工作（`owner_rule` 設空字串**不會**關掉它，空字串＝預設的 5 分鐘），案例會在「工作從沒進 running」處 FAIL 或 SKIP。關掉後 `BuildOwner` 不跑（`cmd/wwd/main.go` 不設 `srcOpt.Owner`，`facts/collector.go` 的 `Owner` 為 nil），案例 7 的 H2 檔案仍會讓 `facts.owner` 變 active（`daemon/sampler.go` 的 H2 區塊），`derived` 的 headroom 歸零，worker 讓位並 reclaim |
| mserv | 不要有 `[fault]`。案例 13 需要：`[worker] sandbox_user = "<job 帳號>"`（非 root）；`[jobdb]`（`enabled = true`、`host`、`port`、`user_hosts`、`defaults_file = "~/.config/wwd/mysql-admin.cnf"`，檔案 0600、帳號 `wwd_admin`）；`wwd.slice` 不用預先建立 | 沙箱與 per-job 資料庫；見 `docs/wwd-deploy.md` |
| air19 | 什麼都不加；worker 必須是關的（precheck 會檢查） | 整個矩陣它都不 claim |

重啟後檢查（每台）：`curl --unix-socket ~/.local/share/wwd/wwd.sock http://wwd/v1/info | jq .fault`，預期 mlab 是 `{"zone":"warning"}`（案例 6 時多一個 `"log_upload_fail":true`）、air26 是 `{"owner_active_file":true,"snapshot_delay_s":8}`、mserv 與 air19 是 `null`；`GET /v1/worker` 在 air26 的 `owner_rule` 非空。

**跑完之後**：把每台的 `config.toml.pre-c2` 放回 `config.toml`（沒有 `[fault]`），重啟 wwd，確認四台 `curl … /v1/info | jq 'has("fault")'` 都是 `false`；`GET /v1/worker` 與矩陣前一致（部署後預設四台都是關的）（**air26 的 `owner_rule` 例外，見下**）；矩陣的暫存、`/tmp/wwd-c2-ran.log`、`fault/owner-active` 腳本結尾已刪，用 §6 的指令確認即可。

**air26 的 `owner_rule`（`[owner] enabled = false` 的副作用）**（只讀程式碼的結論，`internal/wwd/daemon/hoststate.go`）：
- 重啟時 wwd.db 裡存的 `hid<5m` 不會讓 wwd 起不來：`NewHostState` 對從 DB 讀到的設定走 `sane`（hoststate.go:172），`checkWorker` 因 `OwnerRuleSupported=false` 拒絕非空的 owner_rule（:161），`sane` 只在記憶體裡把它清成 `""` 並 log（`owner_rule cleared`），**不會回寫 DB**。
- 所以矩陣期間 `GET /v1/worker` 回 `owner_rule: ""`；腳本一開始存下的就是 `""`，結尾 PUT 回去的也是 `""`，不會被拒（`SetWorker` 的檢查對空字串放行）。
- 但腳本在矩陣中途用 PUT 開關 worker，每次改動都會 `SaveWorker`（:185-），那一筆存的 `owner_rule` 是 `""`，**會把 DB 裡的 `hid<5m` 蓋掉**。所以跑完、把 `[owner] enabled = true` 放回並重啟之後，air26 的 `owner_rule` 是 `""`（規則本身仍照預設 5 分鐘運作，但 `facts.worker.owner_rule` 是空的，worker 也少了「第一次讀數前不接工作」的保護）。
- 跑完要補一步：把 `[owner]` 放回並重啟 wwd 後，`curl --unix-socket ~/.local/share/wwd/wwd.sock http://wwd/v1/worker | jq '.owner_rule = "hid<5m"' | curl --unix-socket ~/.local/share/wwd/wwd.sock -X PUT -d @- http://wwd/v1/worker`，再確認 `GET /v1/worker` 的 `owner_rule` 是 `hid<5m`。腳本在 precheck 看到 air26 的 `owner_rule` 是空的時會印一行提醒。

## 8. 已對照程式碼核對的假設，與第一次真機執行要留意的

腳本原本有 `TODO(first-run)` 標記的假設，已在 2026-10-19 對照 main（`806591dcb`）核對，結果如下：

1. `pdx test --detach --json <task>` 印的是縮排的多行 JSON，有 `.id`（`internal/wwd/testcmd/run.go`、`report.go`）：**對**。
2. `GET /v1/jobs/{id}` 的形狀：`.state`、`.result.reason`、`.result.host`、`.result.started_at`、`.result.summary`：**對**；`.superseded_by` 不在頂層，只在 `.result.superseded_by`（腳本的 `// .result.superseded_by` 備援剛好接住）。`GET /v1/jobs?state=…` 回的是物件 `{jobs,cursor,epoch}`，不是陣列（**已改**）。
3. `PUT /v1/worker` 收的 body 形狀就是 `GET /v1/worker` 給的（嚴格解碼，`owner_rule` 原樣送回必過）：**對**。
4. 缺 log 時 `GET /v1/jobs/{id}/log` 回 404：**對**。
5. 案例 12 被拒絕的 state／reason：state `failed`、`reason` 空、代碼在 `summary.tail[0]`（`task_unknown`／`args_invalid`）：**已改**，腳本現在檢查代碼。
6. 案例 14 與每個案例結尾的 air19 檢查：`jobs.claim_worker` 在 claim 時寫入（值是該 wwd 的 alias），工作 requeue 時（租約過期、reclaim、infra 釋放）清成 NULL，結束時不清；**沒有 claim 事件表**。所以 air19 claim 後立刻被收回、又沒跑到 probe 的短暫 claim 仍抓不到，腳本的第二個來源（air19 自己的 probe log）要保留。
7. 案例 13：per-job 資料庫的環境變數是 `DB_CONNECTION／DB_HOST／DB_PORT／DB_DATABASE／DB_USERNAME／DB_PASSWORD`（預設 `db_env`）：**對**；庫名 `wwd_` 加 32 位十六進位、使用者 `wwdu_` 加 26 位。登入方式**已改**（見案例 13）。
8. air26 找得到 `pdx`（案例 8）：非互動 ssh 的 PATH 在 Mac 上沒有 pdx（只讀 `.zshenv`），所以 precheck 依序找 PATH、`~/.local/bin/pdx`、`~/.config/pdx/bin/pdx`，印出 `pdx on air26: <路徑>`，案例 8 用這個路徑；三處都沒有就 FAIL 並列出三處。

第一次真機執行仍要留意的：
- 案例 5a、7 依賴 §7.1 的 fault 設定（路徑要是字面絕對路徑，zone 要是 `warning`）；設定錯誤的樣子是「掛鉤看起來開了、案例卻沒有效果」，開始前先看 `/v1/info` 的 `fault`。
- 案例 5b 長版、5c 的時間餘裕取決於主機當時的負載（見案例 5c）。
- 案例 13 的沙箱探針假設 job 帳號是非 root、不在 `docker` 以外會讓 `/run/docker.sock` 變成可連的群組裡；`sandbox_user = root` 時 `-w` 測試會失敗（root 無視 mode 000）。

## 9. 測試

- `scripts/wwd-c2-matrix_test.sh`：`--dry-run` 的輸出斷言（每個案例的關鍵步驟與預期值），並用間諜指令證明 dry-run 不呼叫任何命令。
- `scripts/wwd-c2-matrix_stub_test.sh`：用假的 ssh／curl／pdx／sqlite3／launchctl 真跑非 dry-run 的路徑（含清理、清理失敗、pid 檢查、既有檔不刪）。
- 兩個都用 `sh <檔>` 跑，最後一行印 `ok`。
