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
