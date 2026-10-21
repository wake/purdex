# Installing wwd on the four hosts

Steps for the lead's deploy (spec `docs/specs/2026-10-18-wwd-spec.md` §2). Nothing here is run by the
developers: `wwd install` and `scripts/wwd-deploy.sh` write only under the user's home (and, on mserv, one
systemd unit), and every command has `--dry-run`.

| Host | Tailnet | Build | Service | Worker at first |
|---|---|---|---|---|
| mlab | 100.64.0.2 | `darwin-arm64` | LaunchAgent `gs.wake.wwd` | on (heavy 1, light 2) |
| air26 | 100.64.0.4 | `darwin-arm64` | LaunchAgent, owner rule on | on (heavy 1, light 2) |
| mserv | 100.64.0.6 | `linux-amd64` | systemd `wwd.service` (user `wake`) | on (heavy 1, light 1) |
| air19 | 100.64.0.1 | `darwin-amd64` | LaunchAgent | **off** |

The slots and switches are starting values in the generated `config.toml`; change them at run time with
`PUT /v1/worker` (`pdx wwd worker …` once WWD-3c lands), not by editing the file.

## 1. Build

```sh
make build-wwd        # bin/wwd-darwin-arm64, bin/wwd-darwin-amd64, bin/wwd-linux-amd64
```

## 2. The pool token (once)

Every wwd of the pool must hold the **same** token (peers send it as a bearer token). Make it once, on mlab, in
a file nobody else reads; it never goes into the repo, a message or a command line:

```sh
umask 077
openssl rand -hex 32 > ~/.config/wwd-pool-token.new      # 64 hex characters
```

`wwd install --token-file <file>` copies it to `~/.config/wwd/pool.token` (mode 0600) **only when that file does
not exist yet**, and never prints it. An existing token is never replaced; to rotate it, replace
`pool.token` on all four hosts and restart them together (a peer with the old token is unreachable for the
others until then). Without `--token-file`, wwd makes a random token of its own on its first start — fine for
one host, wrong for the pool, so always pass `--token-file` on the first install.

## 3. Install, host by host

```sh
scripts/wwd-deploy.sh --dry-run --token-file ~/.config/wwd-pool-token.new mlab    # look first
scripts/wwd-deploy.sh --token-file ~/.config/wwd-pool-token.new mlab
scripts/wwd-deploy.sh --token-file ~/.config/wwd-pool-token.new air26
scripts/wwd-deploy.sh --token-file ~/.config/wwd-pool-token.new mserv
scripts/wwd-deploy.sh --token-file ~/.config/wwd-pool-token.new air19
rm ~/.config/wwd-pool-token.new
```

The script ships the right build to the host (into a private `mktemp -d` staging directory under `~/.local/share/`, removed when the script ends) and runs
`wwd install --alias <host>` there. On mlab and air26 `pdx setup --wwd --wwd-alias <host> [--wwd-binary <path>]`
does the same through pdx; it takes the `wwd` beside the `pdx` binary unless `--wwd-binary` says otherwise.

What the install does, all of it idempotent (a second run changes nothing and restarts nothing):

1. copies the binary to `~/.local/share/wwd/bin/wwd` (atomic rename; only if the content differs);
2. writes `~/.config/wwd/config.toml` for that host if there is none — alias, tailnet address, worker
   switch and slots, the owner rule on air26, and the other three hosts as `[[peers]]`; an existing config is
   never overwritten;
   **Prepared clones** are not written for you: to give a host `repo:<name>` labels, add a `[repos]` table to
   its `config.toml` and restart wwd (`name = "<clone path>"`, absolute or `~/`):

   ```toml
   [repos]
   purdex = "~/Assist/purdex"
   mlab   = "~/Assist/mlab"
   ```

   **Give wwd a clone of its own; do not share it with the assist runs or any other tool.** wwd writes refs in that clone
   (`refs/wwd/<job id>`: `update-ref` when a job is submitted and when it ends, `fetch` when a worker takes it), and a second
   git user in the same `.git` (a fetch, a gc, a pack-refs) fights it for the ref and `packed-refs` locks. wwd tries a ref
   write again 3 times, 100 ms apart, when git says `cannot lock ref`; that covers a short clash, not a clone that is busy for
   long.

   **The reference label reads this clone's working tree.** A task whose `qualify.reference` is this host is advertised as
   `task:<repo>/<task>` from the manifest in the clone's working tree (`.wwd/tasks.toml`), not from the caller's. After the
   `qualify` or `labels` of a task change on `main`, fast-forward this clone (`git -C <clone> pull --ff-only`), or the host
   keeps advertising the old labels (until #3658 is fixed).

   A path counts only if it is a directory you own that holds a `.git`; otherwise that label is simply not
   given (no error). The check runs with every facts record, so a clone that appears or disappears changes
   the labels (a `labels` event) within 5 s. `GET /v1/info` shows the labels and the toolchain fingerprint;
   **Tools outside the default search path** (for example a toolchain under `~/Assist`): add a `[tools]` table to
   `config.toml` and restart wwd. The directories go in front of the default tool directories, for the facts' tool probe and
   for every worker job's PATH (one list, so a tool the facts report is a tool a job finds). A local fast-lane run keeps the
   caller's own environment and PATH, as before; the PHP probe also looks in Herd's own directory, which is not on a job's PATH. Install does not need to run again: the
   jobs' PATH is built at start, not read from the unit's `Environment=PATH`. A directory that does not exist on the host is
   skipped, with one line in the log:

   ```toml
   [tools]
   extra_path = ["~/Assist/toolchains.noindex/node/bin"]
   ```

   **mserv** needs exactly this entry (node v24.14.0 lives there; without it a job cannot run `node`, and the at-inwin
   `UUIDProcessingTest` fails). After editing, restart wwd (`sudo systemctl restart wwd`, after the release gate and the
   guardian's go-ahead) and check that the host's facts record shows `tools.node`;
3. copies the token (above);
4. writes the service definition — `~/Library/LaunchAgents/gs.wake.wwd.plist` (`KeepAlive`, `RunAtLoad`, log
   in `~/.local/share/wwd/wwd.log`, a PATH that finds go, node, pnpm and PHP) or, on mserv,
   `/etc/systemd/system/wwd.service` through `sudo install` (`Restart=always`, `Slice=wwdd.slice`,
   `CPUQuota=100%` (at most one core), `MemoryMax=512M`, `TasksMax=256`, `ProtectSystem=full`; deliberately no
   `NoNewPrivileges`: the worker starts jobs through `sudo systemd-run` (spec §5), which sudo refuses under that flag,
   and the privilege boundary is sudoers, spec §11). wwd itself runs in
   **`wwdd.slice`**, so it does not sit in `system.slice` among the production services (livekit, mysql, …); the
   worker's jobs run in `wwd.slice` (spec §5), a different slice. The name has no hyphen on purpose: systemd takes
   `wwd-xxx.slice` for a child of `wwd.slice`, which would put wwd's own process in with the jobs. An install over a
   unit made before this replaces it, reloads systemd and restarts wwd into the new slice; check with
   `systemctl status wwd` (the CGroup line reads `wwdd.slice/wwd.service`);
   **On Linux it also limits `wwd.slice` itself** (#3549): the jobs have limits of their own (`CPUQuota=200%`,
   `MemoryMax=4G` each), but the slice they share had none, so two jobs together could take the whole machine and starve
   the production services. `/etc/systemd/system/wwd.slice.d/10-wwd-limits.conf` (through `sudo install -D`; written
   only when it differs) holds `CPUWeight=50` (half the weight of the production services, in contention only),
   `CPUQuota=300%` (at most three of mserv's four cores), `MemoryMax=6G`, `MemorySwapMax=0` (a job that outgrows its
   memory is killed, not swapped) and `TasksMax=4096`. Each value can be overridden, one at a time, in wwd's
   `config.toml` (the file is never overwritten, so the override is the operator's):

   ```toml
   [slice]
   cpu_weight = 50        # 1 to 10000
   cpu_quota = "300%"     # a percentage
   memory_max = "6G"      # 6G, 512M, a byte count or "infinity"
   memory_swap_max = "0"
   tasks_max = 4096
   ```

   A value that is not a plain value is refused (by `wwd install` and by wwd at start) before anything is written: the
   file is read by systemd as root. A changed drop-in is made known with `systemctl daemon-reload`; wwd is **not**
   restarted for it, and the new limits apply to running jobs at once (checked before the first mserv worker is
   turned on, #3549). With `--no-restart` the file is written and the command is printed. `wwd uninstall` removes it.
   The 6G follows from mserv's 15 GB, the production services' 2 GB resident and the 4G one manual run in
   `assist.slice` may take: **`assist.slice` has no limit of its own**, so while a worker is on, a manual run to mserv
   must not run beside it (the 2026-10-20 ruling: `mserv-run.sh` refuses while mserv's worker is on);
5. loads or restarts it: first install → `launchctl bootstrap` / `systemctl enable` + `restart`; a new
   binary → `launchctl kickstart -k` / `systemctl restart`; a changed plist → bootout + bootstrap; nothing
   changed and running → nothing. `--no-restart` does steps 1–4 only and prints the restart command.

mserv needs `sudo` without a password for `install`, `systemctl` and `rm` on the unit (the account `wake` has
it). ufw on mserv already admits the tailnet; wwd listens on 100.64.0.6:7871 and 127.0.0.1:7871 only.
macOS may ask once whether `wwd` may accept incoming connections: allow it (the listener is bound to the
tailnet address and loopback, never to other interfaces).

## 4. Check

On each host (the socket is the local, token-free surface):

```sh
curl -s --unix-socket ~/.local/share/wwd/wwd.sock http://wwd/v1/info          | jq .
curl -s --unix-socket ~/.local/share/wwd/wwd.sock http://wwd/v1/facts         | jq '{alias, derived, worker, owner}'
curl -s --unix-socket ~/.local/share/wwd/wwd.sock http://wwd/v1/pool          | jq '.peers[] | {alias: .info.alias, reachable}'
```

All four peers `reachable: true` means the token and the addresses agree. A peer that is `false` while its
own `/v1/info` answers: the tokens differ, or `alias` in its config is not the name the others list for it.

On Linux (mserv) the limits of the job slice are in place when these five are not the defaults (`infinity`, no weight):

```sh
systemctl show wwd.slice -p CPUWeight,CPUQuotaPerSecUSec,MemoryMax,MemorySwapMax,TasksMax
```

Acceptance (WWD-1): every host reports a full record, and each wwd uses under 1 % CPU and under 50 MB RSS:
`ps -o pid=,rss=,pcpu=,time= -p $(pgrep -f 'bin/wwd -config')`, twice a minute apart (the second `time=`
minus the first is the CPU spent; measured on mlab: about 0.5 % and 18–22 MB).

## 5. Upgrade, roll back, remove

* Upgrade: build, then run `scripts/wwd-deploy.sh <host>` again (no token flag). Check
  `relays_active = 0` and that no wwd job is running first if you restart (`--no-restart` defers it).
  **Back up `wwd.db` first whenever the queue schema moves** (see the table below): the migration runs when the new wwd
  starts and only goes forward.

  ```sh
  sqlite3 ~/.local/share/wwd/wwd.db ".backup '$HOME/.local/share/wwd/wwd.db.bak-$(date +%Y%m%d-%H%M)'"
  ```

  (`.backup` is safe while wwd runs; copying the file by hand is only right with wwd stopped, and then with its `-wal`
  and `-shm` files.)
* **The host schema moves too** (`host_schema` in `wwd.db`; 7 since 8b-3: two nullable columns on `exceptions` for a hold's `allow`
  and `flags`; 8 since #3655: `qualify_attempts`; **9 since ed3920-61: `worker_settings.disk_min_gb REAL NOT NULL DEFAULT 10`**, the
  worker's disk floor. A host that has a `worker_settings` row gets 10 on it, whatever its config says; one without a row follows
  `[worker] disk_min_gb`, unset = 10). An older wwd refuses a file whose host schema is newer than it knows (`wwd.db has host schema 7, this wwd knows 6: a
  newer wwd wrote it`), exactly as it does for the queue schema, so the same back-up and the same roll back apply to it: **a roll
  back across a host-schema step is a restore of the backup**, and a hold made since the backup is lost with it. Check what a host has
  with `sqlite3 -readonly ~/.local/share/wwd/wwd.db "select version from host_schema"`.
  **That refusal covers an older wwd that starts after the migration, not one that already has the file open**: a running v6 wwd does not
  look at `host_schema` again, and its `SaveException` leaves the new columns alone, so a same-named plain exception written by it over a
  hold would keep the hold's `allow` and `flags`. So **the old wwd must be stopped before the new one starts** (`systemctl restart`,
  `launchctl kickstart -k` and the roll back below all stop it first), and **never run two wwd on one `wwd.db`**: the second one
  migrates in `store.Open` and only then fails to bind its port (wwd has no single-instance lock; #3630).
* **Adding a field to a wire payload: the "conversion build" rule** (#3710). Until this change every reader decoded strictly
  (`wire.Decode`): a record with a field the reader's build did not know was refused whole, so every new field was a break for any host
  that was not upgraded first. Readers of what comes from somewhere that may be a later build — a peer's answer or push, an event, a
  record read back from `wwd.db`, the body of a peer's worker call — now use `wire.DecodeLenient` (unknown fields skipped, framing, types
  and `Validate` still checked). Request bodies written by a person or a script (`PUT /v1/worker`, `POST /v1/exceptions`, `/v1/jobs`,
  `/v1/local-runs`, the position calls) stay strict, so a typo is an error; `TestStrictDecodeOnlyReadsRequestBodies` keeps that list.
  The build that carries `DecodeLenient` is the **conversion build** (from #3710; if it ships in 765, 765 is it). The rules:
  1. The conversion build itself adds **no field whose safety depends on the reader being lenient**: the 764 hosts still read strictly,
     and would refuse such a record (`TestAStrictReaderOfTheOldKindRefusesAFieldItDoesNotKnow` pins this).
  2. Before a later build adds a field to a payload, **check that every pool host (mlab, air26, air19, mserv — air19 is often behind) runs
     the conversion build or later** (`pdx wwd status` on each: version; or `GET /v1/info`).
  3. **After a field has been written, do not roll back to a build before the conversion build**, unless the field has not been written
     yet (a host on the old build refuses the record whole, and the pool view of the host goes stale — `pool.go` keeps the last facts).
  4. A new **enumeration value** (a new job state, alert kind, event type, …) is not covered: `Validate` still refuses a value the
     reader does not know. Those need the proto/capability route, or the same deploy-all-first order.
  Fields shipped before the conversion build (`disk_low` and `disk_min_gb` in 765) keep their own rule: same host, Purdex daemon and `pdx`
  first, then wwd, all four hosts together.
* Roll back: deploy the previous build the same way. If the new build moved the queue schema, the old binary refuses
  the migrated database (`queue_schema` newer than it knows), so a roll back across a schema step is: stop wwd,
  put the backup back as `wwd.db` (remove the `-wal` and `-shm` files beside it), start the old build. Jobs
  made since the backup are lost. **If the restored database has a `db_epoch` table (schema 5 or later), give it a
  new epoch before wwd starts**, with wwd stopped:

  ```sh
  sqlite3 ~/.local/share/wwd/wwd.db "UPDATE db_epoch SET id = lower(hex(randomblob(16)));"
  ```

  The epoch names the database file's history. A restored copy has the epoch it had when the backup was made, and a
  client (Purdex's `jobnotify`) that kept a cursor from before the restore would take it as still good once the
  restored queue's counter has caught up with it, and then miss the changes below it. A new epoch makes every client
  start again from `0`. (A backup older than schema 5 has no `db_epoch`; upgrading it again makes a new one.)
* **Roll back across queue schema 9** (`jobs.started_at`, MD-5; same shape as the host schema 9 step of #3706). Schema 9 is one nullable
  column, and a wwd that knows only schema 8 refuses the file, so going back to 765 or earlier is the backup restore: on each host, with
  wwd stopped, put `wwd.db.pre-<version>` (the online `.backup` made before the upgrade) back as `wwd.db`, remove `wwd.db-wal` and
  `wwd.db-shm`, give it a new epoch (the `UPDATE db_epoch` above), start the old build. Jobs made since the backup are lost. Before the
  upgrade, check on each of the four wwd (`GET /v1/pool` → `version`) and on mlab and air26's Purdex daemons (`/api/health` → `version`)
  that all are at 765 or later: a reader older than 765 reads jobs strictly and refuses a record that has `started_at`, which would hide
  every started job from it. The deploy script stops if one is lower.
* **Before any roll back: the quarantine must be empty** (#3373 PR2, from the build where the per-job database is named by
  `source/id`; and again from PR3b, where the sandbox unit is named `wwd-<origin tag>-<id>` and an older build's quarantine
  check refuses a marker whose `Unit` is not exactly `wwd-<id>`, so it would not load the new markers at all). A job whose command was not proven stopped sits in the quarantine with its per-job database, and the older wwd's
  database sweep knows only the names made from the job id alone: it would drop the new-named database of a job that may still be
  running. Do this on each host, in this order, before you start the old build:

  1. Stop the worker taking jobs, so that no new entry can appear while you look, and see that nothing is running:

     ```sh
     S="--unix-socket $HOME/.local/share/wwd/wwd.sock"
     curl -sS $S http://wwd/v1/worker | jq '.enabled=false' | curl -sS $S -X PUT -d @- http://wwd/v1/worker
     curl -sS $S http://wwd/v1/facts | jq -c .worker.running      # wait for {"heavy":0,"light":0}
     ```

  2. Count the quarantine markers (one `*.json` per quarantined job):

     ```sh
     find "$HOME/.local/share/wwd/quarantine" -maxdepth 1 -name '*.json' | wc -l
     ```

     `0` is the only answer that lets you go on. If `find` prints any error (a permission, a missing path that should be there),
     the answer is *unknown*: do not go on. A host that never had a quarantine directory has no markers; `find` says
     "No such file" for it, which is the one error you can read as `0`.
  3. With one or more markers: do not roll back yet. The quarantine sweep removes an entry once its unit is proven gone, and the
     worker being off does not stop that sweep, so wait for the count to reach `0`, or deal with the job first (the marker is JSON; its
     `Unit` field names the systemd unit: stop that unit by hand and let the sweep clear the entry). Then count again.
  4. Only now stop wwd and install the old build. Do not turn the worker back on in the new build in between.

  Today this concerns **mserv alone**, the only host with per-job databases (`[jobdb]`); the other three have no database to lose.
  Going forward, new build over old, needs none of this: a sweep of the new build keeps the old names of a held job.
* Queue schema steps (`queue_schema`; each only forward): 2 `git_pins` (3b-2b), 3 `jobs.lapses` (3b-3a), 4 the origin cursor
  `jobs.rev`/`job_rev` and its triggers (3b-4a), 5 `db_epoch` (3b-4d), 6 `jobs.manual`/`jobs.manual_seq` (manual order; the rev trigger is
  replaced to watch them). The wire grows with them: `GET /v1/jobs` answers
  `{jobs, cursor, epoch}`, and a client that decodes strictly and does not know a field fails on it, so wwd and Purdex are
  always deployed together (a short skew between wwd hosts, while the four are being upgraded one after the other,
  only costs the peers' polling a few failed rounds).
* **Host schema step 5 and wire `proto` 2 (WWD-8 8a-c, spec §13.1)** — deploy **the four wwds and both Purdex daemons from the
  same build**. The decoders are strict, so a release reaches the hosts one after another and the contract goes in two phases:
  - *This release* (proto 2) only teaches every reader the new things: Facts `mem.decompress_rate` and `queue`, Exception `allow` /
    `flags`, the `Alert` payloads, the `alert` event and `GET /v1/alerts`; `/v1/info` says `proto: 2`. Writers still send
    `v: 1` with none of the new fields, so a host still on the old build is never shown a field it would reject. A reader takes
    `404` on `/v1/alerts` as "no alerts yet" (an old host), and a facts record or info of a proto it does not know as an
    unreachable peer.
  - **The writers ship one release after the readers.** The readers (the wire contract, the v5 migration, `GET /v1/alerts`) go out
    first and are deployed on all four wwds **and both Purdex daemons**; only when that release is everywhere does the release with the
    writers go out (the signals, the alerts, the gate's users). The gate cannot do this alone: it sees the peer wwds, not the Purdex
    daemon that reads the same host's wwd, and a Purdex daemon upgraded last would meet a v 2 facts record it cannot read. A writer PR
    must not be merged or deployed in the same release as the reader PRs.
  - *The writers' release* fills the new fields, and only while `wire.PoolAtLeast(peers, 2)` holds: every peer reports
    `proto >= 2`. A peer that is down counts by the last info it gave (kept in `<data_dir>/peers-last.json`, so a restart of the wwd does
    not forget it), which is why a laptop that is mostly off does not shut the gate for everyone; a peer never seen holds it shut. The
    cost: a host rolled back while it was away is believed until the next pool poll (5 s) reads its proto. Until then `POST /v1/exceptions` refuses
    `allow` and `flags` with 400, so no writer can show them to a proto 1 reader.
    Check before relying on it: `curl --unix-socket ~/.local/share/wwd/wwd.sock http://wwd/v1/info` on each host shows
    `"proto":2`. A host rolled back below the contract shuts the gate again.
  - `wwd.db` host schema 4 → 5 adds `wwd_starts` and `alerts` (both `IF NOT EXISTS`, so a second start changes nothing). Take the
    online backup first, on each host, named for the version you are about to run:

    ```sh
    sqlite3 ~/.local/share/wwd/wwd.db ".backup '$HOME/.local/share/wwd/wwd.db.pre-<version>'"
    ```

    Restore (a roll back across this step: the old binary refuses a schema it does not know): stop wwd, put
    `wwd.db.pre-<version>` back as `wwd.db`, remove the `-wal` and `-shm` files beside it, give it a new epoch (the command
    above), start the old build. The rows of the two new tables are lost with the roll back (until 8a-1 they hold nothing).
    Check after the upgrade: `sqlite3 ~/.local/share/wwd/wwd.db "SELECT version FROM host_schema; SELECT count(*) FROM alerts;"`
    prints `5` and `0`.
* **Wire `proto` 3 and the passed-test digest (WWD-5b-2r / 5b-2w, spec §7)** — the same two phases, for one new field: `summary.passed_digest`
  in a job result (`sha256:<64 hex>` of the sorted passed test ids; qualification compares it so that two hosts that ran different tests
  and passed as many are told apart). Decoders are strict, so:
  - *The readers' release (5b-2r)*: every wwd and both Purdex daemons learn the field; `/v1/info` says `"proto":3`. Nothing writes it.
    Summaries are byte-for-byte the old shape (the field is `omitempty`; a test pins that an older strict decoder takes them). The
    **facts version is no longer the protocol version**: `wire.FactsProto = 2` is what facts carry and a facts reader takes, so a
    proto 3 build still writes `v: 2` facts when the proto 2 gate is open (a `v: 3` record would be refused by a proto 2 host; a test
    pins it). **Deploy the four wwds and both Purdex daemons from the same build, one after the other and quickly, and on each host
    the Purdex daemon first, then that host's wwd.** The reason is the local read: a new wwd announces `proto: 3` in `/v1/info`
    and as the first entry of `/v1/pool` (itself), and an old Purdex daemon's strict `Pool.Validate` refuses the whole answer for it,
    so with the wwd upgraded first the Purdex daemon's pool API (`/api/pool/hosts`, facts history and series, the fleet view) answers
    "bad response" until Purdex is upgraded too. (The order of the proto 2 release, Purdex first, is the one to repeat.) Across hosts
    the skew costs less: a proto 2 wwd reads a proto 3 wwd's `/v1/info` as an unknown proto, i.e. an unreachable peer, for the few
    minutes it lasts, the gates stay shut meanwhile and nothing is lost, but the pool view of that peer is missing and anything that
    treats an unreachable peer as news (a host-down alert, once the alert writers are on) may fire for it: do the four back to back.
    Check after each host: the Purdex daemon's `GET /api/pool/hosts` answers 200 with this host first, and
    `curl --unix-socket ~/.local/share/wwd/wwd.sock http://wwd/v1/info` shows `"proto":3`.
  - *The writers' release (5b-2w), one release later*: the three summary parsers fill `passed_digest`, and only while
    `wire.PoolAtLeast(peers, 3)` holds. A writer PR must not be merged or deployed in the same release as the reader PR. A host rolled
    back below proto 3 shuts the gate again.
  - A pair compared with a digest missing on either half is a mismatch, never a count comparison, so until the writers are everywhere
    qualification earns nothing; **the first PR that starts a pair on a production host (5b-2c) waits for the writers' release**.
* Remove: `wwd uninstall` (service definition and binary go; config, token and `wwd.db` stay) or
  `wwd uninstall --purge` (everything under `~/.config/wwd` and `~/.local/share/wwd`). `pdx setup --wwd-remove
  [--wwd-purge]` is the same.
* `wwd.db` holds the worker settings, the exceptions and (from WWD-3) the queue. Its schemas now only grow by
  migration steps (`host_schema`, `queue_schema`): a newer wwd's database is refused by an older binary.

## 6. MySQL for per-job databases (mserv only)

A task with `db = "per-job"` gets its own database on the **MySQL dev instance of mserv** (`mysql-dev.service`, port `3307`; the
database `internal/wwd/jobdb` makes and drops for every job). That instance also holds other projects' development
databases, so wwd does not get a powerful account. MySQL cannot limit `CREATE USER`, `KILL` or the process list to names with a
prefix (those privileges are global), so the account holds **no global privilege**:

* The database `wwdctl` holds seven routines (`internal/wwd/jobdb/wwdctl.sql`): `wwd_create_db`, `wwd_create_user`, `wwd_kill_user`,
  `wwd_drop_user`, `wwd_drop_db`, `wwd_list_dbs`, `wwd_list_users`. Each runs as `root` (`SQL SECURITY DEFINER`) after checking every
  argument against the exact shapes wwd makes: a database `wwd_` + 32 lower-case hex, a user `wwdu_` + 26 hex (the first 26 of its
  database's hash, so a user can only be given its own database), a password of 48 hex, a plain host (letters, digits and `. _ : -`,
  no wildcard). The checks are anchored at both ends (`\A ... \z`), case sensitive and length checked; a wrong argument is refused
  with `SIGNAL`, before any statement is built.
* The account is `wwd_admin`@`localhost` with `GRANT EXECUTE ON wwdctl.*` and nothing else. Its password is random (48 hex) and
  lives only in the client file `~/.config/wwd/mysql-admin.cnf` (mode `0600`, readable by `wake` alone), which `wwd`'s `CLI` passes
  to the `mysql` client with `--defaults-extra-file`; the statements go on standard input, so no secret is on a command line.
* A job's user gets `GRANT ALL PRIVILEGES` on its own database (no `GRANT OPTION`) from `localhost` and the addresses in
  `jobdb.Config.UserHosts` (at-inwin connects to `100.64.0.6:3307`, so that address is in the list).

Install (run on mserv, as `wake`; it uses `sudo mysql` for the root account, which has socket authentication):

```sh
scp scripts/wwd-mysql-setup.sh internal/wwd/jobdb/wwdctl.sql mserv:/tmp/
ssh mserv 'sh /tmp/wwd-mysql-setup.sh --dry-run --sql /tmp/wwdctl.sql install'   # shows every statement; no password
ssh mserv 'sh /tmp/wwd-mysql-setup.sh --sql /tmp/wwdctl.sql install'
```

It loads the routines (replacing the old ones, so it is also how a changed `wwdctl.sql` is deployed), keeps the account only when
its client file works **and** its grants are exactly `USAGE ON *.*` and `EXECUTE ON wwdctl.*`; otherwise it drops the account and makes it
again, with a new password and a new client file (so a run that finds drifted grants also rotates the credential, and anything that holds
the old client file must take the new one). It then calls one routine as the account and prints the account's grants.
Check later with `... status`.

Remove (the account and the database `wwdctl`, hence every routine, and the client file; databases and users of jobs that are
left are listed, not dropped, so let wwd sweep them first):

```sh
ssh mserv 'sh /tmp/wwd-mysql-setup.sh --sql /tmp/wwdctl.sql remove'
```

**What names prove.** The routines accept only names of the shapes wwd makes, so the account cannot touch a project's database, a
`mysql` account or any name of another shape. A name of exactly the shape (`wwd_` + 32 hex) that some other party created would be
taken as wwd's: wwd derives them from a SHA-256 of its own job ids, nobody else has a reason to make one, and planting one takes a
right (`CREATE DATABASE`, `CREATE USER`) that is already more than this account has. That is the accepted limit; a registry of what
wwd made (kept in `wwdctl`, written by the routines) would close it and is a follow-up if the instance ever gets other writers.

By hand, if the script is not at hand: `DROP USER 'wwd_admin'@'localhost'; DROP DATABASE wwdctl;` as root on the dev socket
(`sudo mysql -S /run/mysqld-dev/mysqld.sock`), and `rm ~/.config/wwd/mysql-admin.cnf`.

Check what the account may and may not do (it makes one database and one user of the right shape and drops them again; 35 checks):

```sh
scp scripts/wwd-mysql-probe.sh mserv:/tmp/ && ssh mserv 'sh /tmp/wwd-mysql-probe.sh'
```

It must end with `ALL OK`. It drops its own database and user on every way out (an interrupt, a hang-up, a kill: `rc=130`, nothing
left; checked on mserv) and a run begins by dropping what one that was killed with `SIGKILL`, which cannot be caught, left. It tries, as the account, what it must not do (`CREATE DATABASE`, `CREATE USER`, `KILL`, reading `mysql.user` or
another project's table, the routines with another project's names, an upper-case hex digit, a final newline, quote injections in a
name or a host, a user for a database that is not its own, a password that is not 48 hex), and then the whole life of a job's
database: the job's user works in its own database, sees no other, is refused everything else, a held connection is ended by
`wwd_kill_user`, and nothing is left. The first install on mserv (2026-10-18) found that MySQL 8.4 refuses `BINARY expr REGEXP`; the
routines use `REGEXP_LIKE(expr, pattern, 'c')`.

`go test -c -tags mserv ./internal/wwd/jobdb/` makes the end-to-end test of the Go side (scenario J: Create over `100.64.0.6:3307`, a
re-run starts empty, Drop ends a held connection, Sweep keeps what a live job owns); run it on mserv with `WWD_MSERV_E2E=1`.

## 7. Phone pushes of the wwd alerts (the switch)

The alerts the wwd rules raise (spec §13.3) are written to each host's `wwd.db` and read by the Purdex daemons, which gather the
`digest` ones and send the `push` ones to the phone through the push module. **Sending to the phone is off by default.** It is the host
setting `wwd_alerts` with one field, `push`, kept by each Purdex daemon for itself (mlab's and air26's are separate: whichever of the two
holds the fleet `primary` role, or takes over for a primary without a push module, is the one that sends).

```sh
# turn on / off (needs the admin token; baseRevision is the row's current revision, GET /api/hostconfig key wwdAlerts)
curl -sS -X PUT "$DAEMON/api/hostconfig/wwd_alerts" -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"items":{"push":true},"baseRevision":0}'
```

**To stop the pushes now, set `push` to `false` on both daemons.** It is read at every push, so nothing needs a restart, and it is read
again just before each push leaves the push module's queue, so what still waits there is taken back too. The one thing it cannot recall
is a request that is already on its way to APNs at that moment. A setting that cannot be read counts as off. While it is off the alerts are still written, shown by `GET /v1/alerts` and gathered for the digest;
nothing is recorded as sent, so alerts that still hold when it is turned on again are sent then. `/api/info` shows the state under
`wwdalerts`: `{push: "on"|"off", deliverer: true|false, suppressed: <push-level alerts held back in the last pass>}`, and the daemon's
log has one line each time the state changes.

## 8. Known residents (the "resident growth" rule)

"Resident growth" (spec §13.3) reports a single `system` or `resident` process whose own footprint is at least max(4 GB, 25 % of RAM), the
iOS simulator's processes excluded. A process whose large size is normal on a host can be named in that host's wwd config, and is then
**not reported while the host has at least 50 % of its memory available and the swap in use did not grow over the last 10 minutes**;
when either fails, or the swap trend cannot be read, it is reported as any other.

```toml
[advisor]
known_residents = ["OrbStack Helper", "mysqld"]   # process names as the facts show them; empty (the default): none is known
```

wwd reads it at start, so a change needs `wwd` restarted. Nothing is known until a host lists it, so every large resident is reported at first.
`wwd alerts-replay --db <copy of wwd.db> --work-root <dir> --known "OrbStack Helper,mysqld"` shows what the rules would have raised over the
recorded minutes; copy the database first (`sqlite3 <live wwd.db> ".backup <copy>"`), never point it at the live file.

### The "worker crash loop" rule and `<data_dir>/clean_exit` (#3843)

The rule (push) holds when **3 or more wwd starts in an hour followed a run that did not end cleanly**. A deploy, `systemctl restart wwd`,
`launchctl kickstart -k` and both steps of `wwd install` stop wwd with SIGTERM; wwd leaves the file `<data_dir>/clean_exit` (the start time of
that run) as soon as the signal arrives, and the next start reads it, deletes it and records `clean` in its start record
(`wwd_starts.prev_exit`). A start that finds no file records `unclean`: a panic, the OOM killer, a power cut, and **a manual `kill -9`, which is
counted as one crash**. The first start ever records `first`, and a start that follows a record made by a wwd before this change records
`unknown` (that wwd did not know how it ended: the restart that upgrades wwd is not a crash); neither is counted. There is no schema change.

A start is recorded once wwd has bound its ports: a second wwd, or a mistaken `wwd` command, that cannot bind records nothing and leaves the
file where it is. A crash before the ports are bound (for example a panic in start-up) is not seen by this rule; launchd's and systemd's own
restart counters see it. `wwd help`, `-h` and `--help` print the usage and exit 0; an unknown subcommand or flag prints it on stderr and exits
2; none of them reads the config or opens `wwd.db`.

## 9. Silence settings (spec §13.3, 8a-3)

Two settings decide which absences a host's wwd tells. Both are read at start, so a change needs `wwd` restarted.

```toml
[advisor]
partner_check = true            # only on a host that runs a Purdex daemon (mlab, air26); default false

[[peers]]
alias = "air19"
addr  = "100.64.0.1"
alert_unreachable = false       # default true; air19 is a laptop that sleeps
```

* A peer whose pool reads have failed for **3 minutes** (one number for every host, mserv included) raises `silence:<alias>` on this host's
  list; it clears as soon as the peer answers. A wwd that has just started counts a peer it has not reached yet from its own start.
* With `partner_check`, a Purdex daemon that has not read this host's alerts on the socket for 3 minutes raises `partner_down`. Every
  Purdex daemon reads every host's list every 30 s, so a silence that long means the daemon is down. Do not set it on mserv or air19: they
  have no Purdex daemon and would be told about a partner they never had.
* Removing a peer from `[[peers]]`, or turning its alert off, while its alert is raised leaves that row in `alerts`, because no rule judges it any more; clear it by hand in `wwd.db` (known gap: a peer removed from the config is rare).

## 10. Installed dependencies in a job (the dependency cache; #3620)

A task that declares `deps = ["pnpm-lock.yaml"]` (or `package-lock.json`, `yarn.lock`, `composer.lock`) gets its `node_modules` / `vendor`
before its command starts: the worker clones them from a cache keyed by the lockfile's contents plus the install command and the versions
of the tools that run it (`pnpm`/`node`, `npm`/`node`, `yarn`/`node`, `composer`/`php`, found on the job's PATH); a key it has not seen is
installed once, in that job's workspace, and filed. The install runs under the job's own limits (the sandbox on Linux, background QoS, the
clean environment, the job's workspace as HOME) and its log is `<out>/deps-install`, apart from the job's.

* **A pnpm workspace** (purdex: the root lockfile installs under `spa` and `electron` too): the `node_modules` of each member the lockfile's
  `importers` give dependencies to is kept and cloned with the root's (the task runs in `spa`, and `vitest` is there). npm/yarn/composer
  workspaces are not read for members (only the directory next to the lockfile is kept).
* **The install needs the network** (the package registry): a host that cannot reach it cannot install a key it has no entry for. The first
  job of a lockfile on a host pays the install (minutes for the SPA); later ones clone (seconds).
* **Where**: `deps_root` (default `<data_dir>/deps`) holds `entries/` (the cache) and the package managers' own download caches
  (`pnpm-store`, `npm-cache`, `yarn-cache`, `composer-cache`), which an install that has to happen starts from. Keep it on the same volume
  as `work_root` (APFS clonefile / reflink; elsewhere a copy). The directory is made when a job first needs it, so a host that never runs a
  task with `deps` (mlab's worker off, say) never gets one. On Linux the sandbox is widened for the install only, and only for the package
  manager's own download cache (`pnpm-store` …): the installed sets (`entries/`) and the locks are not writable for an install script, so
  one job's script cannot change what other jobs are given.
* **The key** is the lockfile's content, the install command, the versions of the tools that run it, the platform, **and** what else an
  install reads: next to each lockfile `package.json`, `.npmrc`, `.yarnrc(.yml)`, `pnpm-workspace.yaml`, `composer.json`, each pnpm member's
  `package.json`, and the task's fixed `env` values. A change to the root package's scripts is another entry. A remote task may not take a
  name from the worker's own environment (`env_not_allowed`), so a token that lives in the worker's environment does not reach an install.
* **The download caches are bounded**: `[worker] deps_store_max_mb` (default 4096) per package manager; one over it is emptied before the
  next install that needs it (never under another install that is using it).
* **Go tasks** (`deps = ["go.sum"]`, one `go.sum` per task): the worker, outside the job's sandbox and with its own HOME and git/ssh
  configuration (that is how a private module such as `lab.protype.tw/wake/nexen` is reached), runs `go mod download` into
  `<ws>/.gomodcache`; the job gets `GOMODCACHE` pointing there, `GOFLAGS=-mod=readonly`, `GOPROXY=off`, `GOTOOLCHAIN=local`, `GOWORK=off` and
  no credentials (#3663). It also gets **one shared Go build cache** per Go version on the host, `<deps_root>/gocache/<key>` (the key is
  the `go version` text and GOOS/GOARCH; the newest 2 are kept), so a job compiles only what no earlier job did (#3669). The build cache is
  content addressed and made for several go processes at once, and it is not shared between hosts. A job holds it (a shared lock
  `<key>.lock`) from `Prepare` to `Cleanup`; a Linux sandbox may write it. **Test results in it are not reused**: the job's `GOFLAGS` gets
  `-count=1` (after the task's own flags and `-mod=readonly`; a command that names `-count` keeps its own), because Go's test cache knows
  only the files and variables a test read, not tmux, a socket or an executable; the reuse of whole jobs is the result cache's.
  `[worker] disk_min_gb` (default 10, 0 = none) is the free space the work volume must keep for a **heavy** run to start (a queue job is
  not claimed, an adhoc command is refused with `disk_low`); `pdx wwd worker on --disk-min-gb N` or `PUT /v1/worker` changes it live and
  `wwd.db` keeps it. It replaces the pdx gate's `[gate.admission] disk_min_gb`, which goes with the gate. Hosts without a gate (air19,
  mserv) have the 10 GB floor from host schema 9 on. Roll back across host schema 9 is the backup restore above (an older wwd refuses the file).
  `[worker] gocache_max_mb` (default 10240) bounds one Go version's cache, and `[worker] gocache_prune_hours` (default 24, 1 to 8760) is how long
  an entry may go unused: Go touches an entry's file time when it uses it (at most once an hour), so the worker removes the entries whose time is
  older than that plus that hour (an age pass, at most once an hour). Over the bound it trims from the oldest entry down to 80% of it. Neither needs the cache to
  be free (Go's own trim removes files while other go processes run; a removed entry is a miss and is compiled again). Only a cache that could
  not be trimmed under its bound is emptied, when no job holds it (a cold build is what every job paid before); over twice the bound while jobs
  hold it and it cannot be trimmed, a new job gets no shared cache and builds in its own workspace. The size is looked at, and trimmed, at most
  every 2 minutes, in one walk of the entries (about 0.4 s for 40,000 files on mlab once the directory is warm, 3 s the first time). Go's
  `trim.txt` and `README` are left alone. **A timer trims too (#3833):** the job's pass above runs only when a go job starts, with at most 2000
  removals, which a host that starts few jobs and writes a lot with each (mlab, mostly lint) does not keep up with. The worker's beat (the loop's
  sweepers, once a minute, **also while the worker is switched off**: the cache is on the disk either way) looks at every Go version's cache once
  in 15 minutes, in the background, and trims one that is over its bound down to 80%. When no job holds the cache (the exclusive lock is had
  without waiting) there is no limit on the number of entries, but the work is done 5000 entries at a time (about a second) and the lock is let
  go between two chunks, so a job that waits for the shared lock is kept waiting a second at most; while a job holds the cache the rule is the
  job's pass (only entries unused for 2 hours). The timer never empties a cache. Each trim that removed something logs one line
  (`the build cache … trimmed on the timer: N entries, M MB freed, now X MB (bound Y MB, exclusive|shared)`). If the cache is ever doubted, remove `<deps_root>/gocache`: the worker makes it again. Go's own trim
  removes entries unused for 5 days; it does not bound the size (air26's old shared `gobuild` reached 142 GB).
* **Disk**: `[worker] deps_keep` (default 2, 1 to 50) is how many installed sets are kept; each is a whole `node_modules`/`vendor` tree
  (purdex's SPA is several hundred MB), and the download cache comes on top. Set it to 1 on a host short of disk. The oldest set is removed
  when a newer one is filed; removing `deps_root` by hand is safe (the next job installs again).
* **An install that fails** is the worker's trouble: the job goes back to the queue (and the repo is held off on that origin for a while, as
  for any infrastructure failure). The same key failing **twice in a row on a host** ends the next job of it: failed with no exit and the
  reason `deps_install_failed: …` (which no outcome and no qualification counts), and the host does not try that key again for 10 minutes.
  Restarting wwd forgets this. A cancelled job or a stopping worker is not a failure of the install.

## 11. The tools a worker must have, and raising Go (#3546)

The manifest (`.wwd/tasks.toml`) says what a task needs of its host: a task that runs `go` (`go test …`, `make lint`) carries
`tool:go<major>.<minor>` (the `go` line of `go.mod`), one that runs `pnpm` carries `tool:node24` and `tool:pnpm10` (the worker
advertises the major only; `tsc` has them besides its earned `task:purdex/tsc`). A host advertises such a label only when its worker's PATH really finds the tool, so **every worker host needs, on the PATH
of its jobs, go, node and pnpm** — a `[tools]` `extra_path` (§3) when they live outside the default directories. A host that lacks
one does not claim those tasks.

**`tool:claude` (#3908).** `mod-hooks` runs `claude plugin test cmd/pdx/plugin/purdex`, so it carries `tool:claude`. The facts probe `claude --version`
on the job PATH (the same list as go, node and pnpm), and a host advertises `tool:claude` — no version in the label — only when it finds it; the
fingerprint keeps the major only, because Claude Code ships a patch nearly every day and a patch must not take back the labels a host earned. A
host without `claude` on that PATH does not claim `mod-hooks`. The test needs no login and no network and runs from an empty HOME (measured with
`HOME` set to an empty directory and a wwd-like PATH: 817 tests pass in about 130 s; it only writes `~/.claude.json`).

On a host whose `claude` is in `~/.local/bin` (air26) do **not** put the whole `~/.local/bin` into `extra_path`: make a directory that holds
only the link, and list that one:

```sh
mkdir -p ~/Assist/toolchains.noindex/claude/bin
ln -s ~/.local/bin/claude ~/Assist/toolchains.noindex/claude/bin/claude
```

```toml
[tools]
extra_path = ["~/Assist/toolchains.noindex/node/bin", "~/Assist/toolchains.noindex/claude/bin"]
```

Restart wwd, then check `GET /v1/info`: `labels` must show `tool:claude`. The link follows `claude`'s own updates (it points at `~/.local/bin/claude`,
which points at the current version).

**A task whose command is not on the PATH (#3908).** When the program of a task is not found on the worker's PATH (the run ends `start_failed` with
"not found in PATH"), the job goes back with reason `infra` as before (the origin counts the attempt, so `job_requeued` sees it), but the worker
holds off **that task** of that origin's repo, not the repo: its other tasks keep being taken. The wait is 15 seconds, doubled by each failure in a
row, up to 5 minutes, and ends when a run of the task finds its command; it lives in memory, so restarting wwd starts again at 15 seconds. The
log has one line when the hold begins (origin, repo, task, and when the task may be taken again) and one when it is first noticed to be over.
"Not found" means nothing of that name is on the PATH at all; a candidate that is there and cannot be used (a file that is not executable, a link
to nothing, a PATH directory the worker cannot read) is the host's trouble and holds the repo. Every other way not to start (a workspace or HOME
that cannot be made, a sandbox unit systemd could not set up) and every other infra failure
still holds the whole repo for 5 minutes, because those say the host is broken. Another host is not affected: it takes the task if its labels
fit. To fix a host that lacks the command, give it the tool (§11) rather than waiting out the hold.

If **no** host has all the labels of a task, `pdx test` can end at once with exit 5 ("no host can take it") — but only when the pool
was read, no reachable host is still settling its facts, and no host holds the labels, **counting a host that is offline or has its
worker off by the last labels it reported** (those stay). In every other case the job is queued as usual and may wait until the queue
timeout (exit 6). Do not count on the quick failure.

**Raising Go to a new minor** (for example 1.26 → 1.27), in this order:

1. Upgrade Go on all three worker hosts (mlab, air26, mserv) first and check that each host's `GET /v1/info` shows `tool:go1.27`.
2. Then change the `go` line of `go.mod` **and** every `tool:go1.26` in `.wwd/tasks.toml` in one PR.

`TestPurdexManifestTasksCarryTheToolsTheyRun` goes red when the two disagree, so it cannot be forgotten. Between the steps a Go task is
taken by the hosts that have the label it carries: while some hosts are on 1.27 and the manifest still says 1.26, only the hosts still
on 1.26 take it (when all three are raised, none does until step 2 lands); after step 2, a host that has not been raised takes none.
Raise all three before step 2, and do step 2 promptly.

## 12. mserv takes purdex's A-class work (S1-b)

`tsc` and `spa-related` carry no `os:darwin`: they run on a host that holds `repo:purdex`, `tool:node24`, `tool:pnpm10` **and** the earned
`task:purdex/<name>` (spec §7; `purdex_manifest_test.go` keeps the list of such tasks). To make mserv one of those hosts (the lead runs
these; wwd is restarted only after the release gate and the guardian's go-ahead):

1. **A prepared clone of its own** (not `~/Assist/repos.noindex/purdex`: that one belongs to the manual `mserv-run.sh` and is on an old commit).
   Same way as air26's `purdex-wwd`, over anonymous https (the repo is public):
   `ssh mserv 'git clone https://github.com/wake/purdex.git ~/Assist/repos.noindex/purdex-wwd'`.
2. **`~/.config/wwd/config.toml`** on mserv: add a `[repos]` table, and edit the **existing** `[tools]` table (node is already in it; `pnpm`
   lives in `~/Assist/toolchains.noindex/pnpm/bin`, which it does not list yet). `extra_path` belongs to `[tools]`: a line added after
   `[repos]` would be read as `repos.extra_path`.
   ```toml
   [tools]
   extra_path = ["~/Assist/toolchains.noindex/node/bin", "~/Assist/toolchains.noindex/pnpm/bin"]

   [repos]
   purdex = "~/Assist/repos.noindex/purdex-wwd"
   ```
3. **Make sure the worker is off before and after the restart.** The saved worker settings (`wwd.db`) win over `config.toml`, so a restart
   keeps whatever the daemon was last told, whatever `enabled` says in the file. On mserv:
   ```sh
   S="--unix-socket $HOME/.local/share/wwd/wwd.sock"
   curl -sS $S http://wwd/v1/worker | jq '.enabled=false' | curl -sS $S -X PUT -d @- http://wwd/v1/worker
   curl -sS $S http://wwd/v1/worker | jq .enabled      # must print false
   ```
   Then **restart** (`sudo systemctl restart wwd`), check `.enabled` is still `false`, and check `GET /v1/info`: `labels` must show `repo:purdex`, `tool:node24`, `tool:pnpm10`.
4. **The worker stays off until S2 retires `mserv-run.sh`** (a manual run and a worker must not run beside each other). When it goes on
   (the same call with `.enabled=true`; check `GET /v1/worker` again), the coordinator queues one qualify pair per task that has a `qualify` and a reference host (air26) and that mserv does
   not hold yet: `tsc` (the whole run) and `spa-related` (`src/lib/crypto-hash.test.ts`). The pair needs mserv's worker **and** air26's
   worker on at the same time. `pdx wwd qualify --status` shows the outcome; `task:purdex/tsc` and `task:purdex/spa-related` appear in
   mserv's labels once the pair agrees, and only then does mserv claim those jobs. The first job runs `pnpm install` into the dependency
   cache (§10), so it needs network.

## 13. Switching a host's entry on (S2)

The hook of the Purdex mod asks `pdx entry match` about a test or build command **only on a host whose installed mod has the entry switched on**:
the `entry` key of the mod's `pdx.json` (the file beside its `VERSION`, written by `pdx setup`) holds a non-empty string. It is off by default, and it is
the one key `pdx setup` never overwrites, so it is set by hand, once per host, and survives later installs. A missing key, an empty string, a number or
`null` all mean off (the call goes the old way).

* The switch is per host and covers **every** session on it, whatever the repo. A repo with no `.wwd/tasks.toml` is left alone (the answer is `none`).
* `[entry].mode` of the repo's manifest (purdex: `rewrite`) only decides what a session **with the entry on** gets for a command no task declares: an
  ad-hoc job (`pdx test --adhoc`, a heavy slot, this host, never queued) for a single plain command, advice (it runs as typed) for anything else.
  The manifest alone switches nothing on.
* A command that is a declared task becomes `pdx test <task>`. A task marked `local = true` (the development tasks: a single package `go test`,
  `vitest run <files>`) takes the local fast lane: no queue, no background move. A task that is not local (the merge and release tasks, `e2e`
  among them) goes through the queue, may wait for a host, and the hook then runs the call in the background. The fast lane still takes its slot
  from the gate (spec §8.3; moving it to wwd's own slots is part of retiring the gate).
* A command is a task only when it says what the task says about **which tests run**: `-run`, `-skip`, `-tags`, `-short`, `-bench` and `-fuzz` of the
  task must be in the command with the same value. `go test -tags e2e ./cmd/wwd/` is not the `e2e` task (it would run less), and runs as typed or
  as an ad-hoc job. A flag written as `-flag=value` is compared as
  one word, so `-tags=e2e` does not match a task that has `-tags e2e`: it falls to the ad-hoc path.
* **Flags that only say how to run** (`go test` only: `-count`, `-v`, `-p`, `-parallel`, `-timeout`, `-json`, `-failfast`) do not keep a command from its
  task when the task does not write them: they are dropped, and the task's own command runs. So `go test -count=1 ./...` is `go-all`, and the typed
  `-p`/`-timeout` are **replaced by the task's own values** (`-count=1` is added by the worker anyway). **To keep your own `-p` or `-timeout`, use
  `pdx test --adhoc -- go test …`.** (Go reads `--flag` as `-flag`, and so does the entry, for every check on a `go test` command; a lone `--`
  or `-args` ends the flags.) Any other flag changes what runs or what it means (`-race` is its own task, `go-race`; `-cover`, `-shuffle` …): the
  command is then no task, and an ad-hoc job.
* **A pattern that expands is not a package.** The fast-lane task (`go-pkg`, `local = true`) takes packages by name; a pattern with `...` is never
  its. `go test ./...` (the whole tree) is `go-all`, the task that spells `./...` out, a release task through the queue; `go test ./internal/...` and other
  recursive patterns are `go-changed` (merge, through the queue); `go test -race ./...` is `go-race`. A task with a count limit
  (`local = "packages<=N"`) is also queued, not run here, once an argument is a pattern. The check on a local-only task is in `internal/wwd/entry`
  (`matchTask`), not in the manifest; `TestMatch_NoRecursivePatternIsEverLocal` keeps it for purdex's.
* A command that is a queued task is rewritten only when some host can take it **now**: `pdx entry match` reads the local wwd's pool (2 s) and a
  host counts when it holds all of the task's labels, answers, and has its worker on (an owner at the keyboard making it yield is waiting, not a
  no). A host that holds the labels but is down or has its worker off does not count. A host that answers and has its worker on but has not sent its first facts yet (the first seconds after wwd starts, when only its `os:` and `arch:` labels are announced) leaves the answer open and the rewrite stays, as `pdx test` queues in that case. Otherwise the answer is `none` and the command runs as typed:
  the alternative is `pdx test` ending at once with exit 5 (aigora-v2's `vitest-root` needs `repo:aigora-v2`, which no host has), or a call moved to
  the background that waits for a down host up to the queue timeout. A pool that cannot be read is `none` too. The reason (one line, for example
  `no reachable host with a worker on holds repo:aigora-v2`) is in the JSON answer only; the hook does not print it. Local-lane tasks and ad-hoc jobs
  need no other host and are not judged.
* **Known gap**: the pool does not show a host's `explicit_labels_only` (a worker setting: mserv takes only jobs whose labels name its `os:` label,
  a `task:` label it earned or a `host:` label). Labels that such a host satisfies, while it will not take the job, still pass the check above, and the call then
  waits in the background until the queue timeout. Purdex does not meet it: `purdex_manifest_test` makes every queued task carry `os:darwin` or its
  own `task:` label. When another repo does, the setting has to become a field of the host's info.
* Everything fails open: a `pdx` that does not answer, an answer that is not JSON or a manifest that does not parse lets the command run as typed.
* What a fast-lane job records (#3735): `snapshot.commit` is HEAD (a reference only), `snapshot.tree` is the tree of the working directory **as it was when the run was
  registered** (uncommitted and untracked files included, ignored ones left out; HEAD's tree when nothing differs). It is taken in a temporary index with its objects in a
  temporary directory, so nothing is written into the repository; it costs about 0.5 s in purdex (6.5k tracked files). A run that waited for a lock or a slot takes it again when it goes on; a file edited while the command runs, or while it waits for its slot after being registered, is not in it. A submodule's own uncommitted changes are not in it either (the tree holds the gitlink; #3743).
  When the tree cannot be taken the run goes on and the record has an empty `snapshot.tree`, with the reason in the run's report notes: it is never HEAD's tree.

## 14. The release gate (S2-b)

The gate is `scripts/release-gate.sh <commit>`: every release task of `.wwd/tasks.toml` for that commit, through `pdx test`, and one table.

```sh
scripts/release-gate.sh [--no-cache] [--mod] [--skip <task>]... <commit>
```

* **The tree.** `pdx test` sends the working tree of the repository it runs in and has no option for a commit, so the gate keeps a clean clone of
  its own, `~/.local/share/purdex-gate/clone`, whose `origin` is GitHub (`git@github.com:wake/purdex.git`, never the main checkout: its local `main` is
  often behind the cut, and `--reference` would lean on its objects). Each run fetches, checks the commit out detached, and removes everything the
  commit does not have (`clean -ffdx`: a snapshot carries untracked files). The commit is given as a **hash** (7 to 40 hex digits, never a branch name)
  and has to be on a branch of the remote after that fetch (a commit that only the clone has is refused). One run per clone: a lock
  of the system (`lockf`, or `flock`, on `<clone>.lock`), held by a small process that ends when the script does, however it ends, refuses a second
  run (exit 2) and cannot go stale; every run writes to a directory of its own. A TERM or Ctrl-C ends the `pdx test` calls too (the jobs they sent
  stay in the queue; TERM first, KILL for what is still there after 10 s) and the gate exits 130. When the
  tasks are done the clone must still be at the commit, or the gate fails.
* **The tasks**, all sent at once, each by its own `pdx test <task> --json` that waits for its job (there is no `pdx jobs wait`, and `--json` has the
  job id, the host and `cache_hit`): `go-all`, `spa-full`, `tsc-force`, `e2e`, `lint`, `wwd-linux`, `wwd-linux-vet`, and the scripts' own tests
  (`wwd-scripts-mysql`, `-c2`, `-c2-stub`, `-deploy`, `-gate`). `--mod` adds `mod-all`: send it only when `cmd/pdx/plugin` changed.
  `purdex_gate_test.go` keeps the script's list and the manifest in step.
* **The table** has one row per task: pass or FAIL, the job's exit, the host it ran on, the job id, `ran` or `cache`, the seconds. The last line counts
  them. A pass is `pdx test`'s exit 0 **and** a report that says so (this task, `succeeded`, exit 0, with a job id and a host); a task that gave no
  report (pdx died, wwd was down) or a report that does not, is a FAIL. Logs of each `pdx test` call are in `~/.local/share/purdex-gate/runs/`.
* **The cache.** A result that was filed for the very same snapshot tree (the key is the whole tree, the task and its definition, and the host's
  toolchain) answers at once and shows as `cache`: what was not touched keeps its last result. Only successes are filed. `--no-cache` runs everything
  again.
* **Exit**: 0 every task passed · 1 a task failed · 2 bad usage or the clone could not be made · 3 nothing failed, but a task was skipped.
* **`--skip <task>`** (repeatable) leaves a task out, shows it as `跳過` and is never counted as a pass; the gate is then INCOMPLETE (3). Without
  it, a task that no host can take ends with exit 5 and fails the gate. Until mserv is set up (below), `--skip wwd-linux --skip wwd-linux-vet`.
* **`e2e` starts daemons**, and its labels (`os:darwin`, `tool:go1.26`) are met by mlab and air26 alike, so either may take it. To make it air26's:
  put `host:air26` in its `labels` in the manifest (every wwd advertises `host:<alias>`; one line, merged like any change, and the job then waits for
  air26 when it is busy), or for one run switch mlab's worker off (`pdx wwd worker off` on mlab) and on again after. Today nothing pins it.

**mserv takes `wwd-linux` and `wwd-linux-vet`** once it is set up (§12 has the first half). Besides what §12 says, the Go that the tasks run:

1. `~/.config/wwd/config.toml` on mserv: `[tools] extra_path` lists node and pnpm (§12) **and** `~/Assist/toolchains.noindex/go/bin`
   (Go 1.26.0 is there; it is not on the default path), so the worker advertises `tool:go1.26`.
2. The Go modules come from the worker, not the job (`deps = ["go.sum"]`): the worker runs `go mod download` outside the sandbox with `GOPRIVATE=lab.protype.tw`,
   and the job runs offline. The private module (`lab.protype.tw/wake/nexen`) needs credentials for `lab.protype.tw` in the **worker's** environment on mserv;
   without them the first job fails at the download. Check it with one `pdx test wwd-linux-vet` before the gate relies on it.
3. The jobs run in `wwd.slice` (3 cores, 6 G of mserv's 4 cores and 15 GB). The old run kept to 2 cores and 4 G in `assist.slice`.
4. `explicit_labels_only = true` stays: the tasks name `os:linux`, which is what the worker accepts. The worker goes on only after the manual `mserv-run.sh`
   is retired (the two must not run side by side).

## 15. The watcher's pressure rules: switching them on, and back (S3-2)

`pressure_watch`, `pressure_danger` and `swap_growth` (spec §13.3) write a level (`watch`) and three kinds that an older reader of the alert list does
not know, and such a reader refuses the **whole** list (`DecodeLenient` still validates). wwd refuses its own stored list the same way after a
rollback. So the build that can read them comes first, and the switch that makes a host write them is a config line, off by default.

**Order of the releases.** S3-2a (the readers, the config keys, the sampler's signals, this section) goes out first; the three rules themselves are
S3-2b. Until S3-2b is deployed, `watch_rules = true` is accepted and does nothing: no alert of the new kinds is raised. So do not read a quiet
list as "the watching is on" before S3-2b is on every host. The steps below are for the build that has the rules.

```toml
# ~/.config/wwd/config.toml
[advisor]
watch_rules    = true                                # default false: none of the three is judged
off            = ["pressure_watch", "swap_growth"]   # optional: rules this host does not judge (these three kinds only)
swap_growth_mb = 750                                 # optional: the 30 min growth that raises swap_growth; default by RAM (500 MB to 16 GB, 750 MB at 24 GB)
```

**Switching on.** Only when **every** reader has the build that knows the new kinds: all four wwds (mlab, air26, air19, mserv) and both Purdex
daemons (mlab, air26). Then, host by host: add `watch_rules = true` (and `off` where it applies) and restart that wwd. air19 does not turn them on
(its worker is off and its VM is resident, so the rules mean nothing for scheduling); if it ever does, it lists `off = ["pressure_watch",
"swap_growth"]`. `pressure_danger` is a push alert: while `wwd_alerts.push` is off (§7) it only wakes the watcher.

**Going back to an older build.** The other way round: on every host set `watch_rules = false` and restart that wwd, then wait for the alerts of
the three kinds to be gone from `GET /v1/alerts` (a host that no longer judges a rule clears its own active alert at the next pass, within 30 s);
only then install the older build. An older wwd that finds a row of a kind it does not know in its own `alerts` table refuses the whole list; if
one is left (the wwd died before it cleared), delete the rows of those three kinds in `wwd.db` before starting the older build.

**Checking.** `GET /v1/alerts` on the host's wwd socket (or the App's pool alert view, `GET /api/pool/hosts/{alias}/alerts`) lists the active alerts; a host with the rules on and calm shows none of the three. `wwd alerts-replay` (§8) will count the three rules over recorded minutes once S3-2b is in.

## 16. The queue's four rules: switching them on, and back (S3-3)

`queue_stalled`, `job_requeued`, `queue_backlog` and `queue_unclaimable` (spec §13.3, level `watch`: they wake the watcher and are neither pushed nor put
in the digest) tell what the daemon's patrol used to see by looking at the queue. Like §15 they are kinds an older reader of the alert list refuses
whole, so the build that reads them (S3-3a, which skips what it does not know) comes first and the rules are a config line, off by default.

```toml
# ~/.config/wwd/config.toml
[advisor]
queue_rules = true   # default false: none of the four is judged
```

| kind | holds when | clears when |
|---|---|---|
| `queue_stalled` | a job not on hold has not changed for 5 minutes and a host that answers, has its worker on, takes it by its labels and has a heavy slot free is there | no such job |
| `job_requeued` | a job (on hold or not) has been put back three times or more (`attempts ≥ 3`; a lapse of the lease is one of them, a reclaim is not) | no such job |
| `queue_backlog` | the oldest job not on hold was submitted 20 minutes ago or more | no such job |
| `queue_unclaimable` | a job not on hold was submitted 10 minutes ago or more and no host that answers with its worker on takes it by its labels | no such job |

"Takes it by its labels" is the worker's own rule (`pick.Eligible`): the job's labels are among the host's, and a host with `explicit_labels_only`
(mserv) takes only a job that names an `os:`, `task:` or `host:` label. "A heavy slot free" is the worker's own reading of the host's facts
(`derived.HasHeavyRoom`). A label that was quarantined is not among the host's labels. A host that is down takes nothing.

**What the clock is.** `queue_backlog` and `queue_unclaimable` count from the submission (`queue.QueuedJob.CreatedAt`). `queue_stalled` counts from the
submission too, except for a job that was already put back (attempts above 0): it counts from the job's last change (`queue.QueuedJob.Touched`). So a job
that was put back has its wait counted again when a person moves it or asks to cancel it: 已被放回過的 job，被手動移動或取消時，(1) 的等待時間會重新起算
(at most the five minutes of the threshold later). The table keeps no separate time of the last put-back (`eligible_since` is in the Backlog, for a batch
that may change the schema). All of them are for the watcher, who is awake anyway.

**During a release** the workers of the pool are held (release hold) or off: a job that waits more than ten minutes then makes `queue_unclaimable` sound.
That is expected and not a fault; the watcher is told, nobody is pushed. The alert clears by itself when the workers are back.

**Which host.** Each wwd judges the jobs queued **on itself** against the pool it sees. A host that nobody submits to has an empty queue and the rules
hold nowhere on it. Turn them on where jobs are queued; `GET /v1/alerts` of that host lists them.

**Switching on.** Only when **every** reader has the S3-3a build: all four wwds (mlab, air26, air19, mserv) and both Purdex daemons (mlab, air26). The
information a host announces about its worker (`explicit_labels_only` in `/v1/info`) also comes with S3-3b-1; a peer without it is taken to take any job
its labels hold, so `queue_unclaimable` sounds less, never more, until it is upgraded. Then, host by host: add `queue_rules = true`, restart that wwd,
and ask the keeper (lucia) whether RUNNING is 0 first, as for any restart.

**Going back.** On every host set `queue_rules = false` and restart that wwd, then wait for the four kinds to be gone from `GET /v1/alerts` (a host that no
longer judges the rules clears its own active alerts at the next pass, within 30 s); only then install an older build. A build from before S3-3a that
finds a row of these kinds in its own `alerts` table refuses the whole list; if one is left (the wwd died before it cleared), delete the rows of the
four kinds in `wwd.db` before starting the older build, as in §15.

**Checking.** With the rules on and the queue calm, none of the four is in `GET /v1/alerts`. `wwd alerts-replay` (§8) cannot judge them: a recording has no
queue, and a rule without its input is counted as "could not be judged", not as quiet.

## 17. The old gate's shims (WWD-9 9a-1)

The host-load gate's shims (`go`, `pnpm`, `npx`, `vitest`, `tsc`, `composer`, `xcodebuild`, `swift`, `swift-build`, `swift-test` in
`~/.config/pdx/shims`) are retired. `pdx setup --agent cc` removes the ten files an old setup wrote there (a file counts only if its second line is
the shim header) and then the directory, if nothing else is in it; a file of another name, or one of those names that is not a shim, is left, with a
line saying so. `pdx setup --uninstall-gate` is gone: its work is part of the setup now. The `~/.zshenv` block was taken out by the S0-b setup on
every host that had one (mlab and air26, read back 2026-10-21; air19 and mserv never had shims), so nothing removes it any more.

A shell that started before the setup still has the shim directory on its `PATH`. If it reports `command not found` for `go` and the like, run
`rehash` or open a new shell. Until then `pdx gate` still skips a leftover shim when it looks for the real tool, so nothing loops.
