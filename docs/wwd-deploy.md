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

   A path counts only if it is a directory you own that holds a `.git`; otherwise that label is simply not
   given (no error). The check runs with every facts record, so a clone that appears or disappears changes
   the labels (a `labels` event) within 5 s. `GET /v1/info` shows the labels and the toolchain fingerprint;
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
* Queue schema steps (`queue_schema`; each only forward): 2 `git_pins` (3b-2b), 3 `jobs.lapses` (3b-3a), 4 the origin cursor
  `jobs.rev`/`job_rev` and its triggers (3b-4a), 5 `db_epoch` (3b-4d), 6 `jobs.manual`/`jobs.manual_seq` (manual order; the rev trigger is
  replaced to watch them). The wire grows with them: `GET /v1/jobs` answers
  `{jobs, cursor, epoch}`, and a client that decodes strictly and does not know a field fails on it, so wwd and Purdex are
  always deployed together (a short skew between wwd hosts, while the four are being upgraded one after the other,
  only costs the peers' polling a few failed rounds).
* Remove: `wwd uninstall` (service definition and binary go; config, token and `wwd.db` stay) or
  `wwd uninstall --purge` (everything under `~/.config/wwd` and `~/.local/share/wwd`). `pdx setup --wwd-remove
  [--wwd-purge]` is the same.
* `wwd.db` holds the worker settings, the exceptions and (from WWD-3) the queue. Its schemas now only grow by
  migration steps (`host_schema`, `queue_schema`): a newer wwd's database is refused by an older binary.

## 6. Fault hooks for the C2 matrix (test hosts only, off by default)

`docs/wwd-c2-matrix.md` §7 needs four hooks to make a host misbehave in one known way. They live in `[fault]` of
`~/.config/wwd/config.toml` and **nothing else turns them on**: no route, no flag, no environment variable. A hook works only
when `enabled = true` **and** its own key is set; a key without `enabled = true` makes wwd refuse to start (a typo that
would silently do nothing is worth a loud failure).

| Key | Hook | Case | Turn it on at |
|---|---|---|---|
| `log_upload_fail = true` | H1: the origin answers 503 to a log upload (a result upload is not touched) | 6 | mlab (origin) |
| `owner_active_file = "/abs/path"` | H2: while the file exists the worker's headroom is empty, as if the owner were active: `facts.owner.active` becomes true (reason `fault: owner_active_file`), `sessions_room` keeps its real value | 7 | air26 |
| `zone = "danger"` (or `warning`) | H3: `facts.derived.zone_override`. The real `zone` is unchanged, so the Purdex gate feed, the app's host cell and the headroom do not see it; only a worker's pick (`Derived.PickZone()`) scores by it | 8 | mlab |
| `snapshot_delay_s = 20` | H4: the worker waits this long (≤ 3600) before a snapshot fetch | 5a | air26 (worker) |

Every start with a hook on logs `wwd: FAULT HOOKS ARE ON`, and every trigger logs a line (H3 only at start: it applies to each
sample). `GET /v1/info` has a `fault` object naming what is on; it is absent when nothing is. The matrix script reads it to
decide SKIP.

**Turn on** (per host, only the ones the cases you run need):

```sh
# mlab; air26 the same with its own keys. The file is the whole of it: edit, restart.
printf '\n[fault]\nenabled = true\nlog_upload_fail = true\nzone = "danger"\n' >> ~/.config/wwd/config.toml
launchctl kickstart -k gui/$(id -u)/gs.wake.wwd          # mserv: sudo systemctl restart wwd.service
curl -sS --unix-socket ~/.local/share/wwd/wwd.sock http://wwd/v1/info | jq .fault
```

**Turn off** (after the run; do not leave a host with a hook on):

```sh
# delete the whole [fault] table from config.toml, then restart as above
curl -sS --unix-socket ~/.local/share/wwd/wwd.sock http://wwd/v1/info | jq 'has("fault")'   # must print false
rm -f ~/.local/share/wwd/fault/owner-active                                                  # H2's file, if the run made one
```

While H3 is on at mlab, `pdx wwd pool` and `/v1/facts` show a `zone_override` for it; nothing else on mlab changes (the gate
keeps reading the real zone). While H2 is on at air26, `free` shows 0/0 for it, which is what a real active owner shows.
