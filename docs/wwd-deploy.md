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

The script ships the right build to the host (`~/.local/share/wwd/incoming/wwd`) and runs
`wwd install --alias <host>` there. On mlab and air26 `pdx setup --wwd --wwd-alias <host> [--wwd-binary <path>]`
does the same through pdx; it takes the `wwd` beside the `pdx` binary unless `--wwd-binary` says otherwise.

What the install does, all of it idempotent (a second run changes nothing and restarts nothing):

1. copies the binary to `~/.local/share/wwd/bin/wwd` (atomic rename; only if the content differs);
2. writes `~/.config/wwd/config.toml` for that host if there is none — alias, tailnet address, worker
   switch and slots, the owner rule on air26, and the other three hosts as `[[peers]]`; an existing config is
   never overwritten;
3. copies the token (above);
4. writes the service definition — `~/Library/LaunchAgents/gs.wake.wwd.plist` (`KeepAlive`, `RunAtLoad`, log
   in `~/.local/share/wwd/wwd.log`, a PATH that finds go, node, pnpm and PHP) or, on mserv,
   `/etc/systemd/system/wwd.service` through `sudo install` (`Restart=always`, `MemoryMax=512M`,
   `TasksMax=256`, `NoNewPrivileges`; the worker's jobs run in `wwd.slice`, not in this unit);
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
* Roll back: deploy the previous build the same way.
* Remove: `wwd uninstall` (service definition and binary go; config, token and `wwd.db` stay) or
  `wwd uninstall --purge` (everything under `~/.config/wwd` and `~/.local/share/wwd`). `pdx setup --wwd-remove
  [--wwd-purge]` is the same.
* `wwd.db` holds the worker settings, the exceptions and (from WWD-3) the queue. Its schemas now only grow by
  migration steps (`host_schema`, `queue_schema`): a newer wwd's database is refused by an older binary.
