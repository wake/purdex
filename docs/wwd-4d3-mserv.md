# WWD-4d-3: the Linux sandbox measured on mserv

2026-10-18, mserv (Ubuntu, 4 cores, PHP 8.5.11, wwd.service already running), as `wake`. The tests are `internal/wwd/run/sandbox_mserv_test.go` (build tag `mserv`, runs only with `WWD_MSERV_E2E=1`): they call `run.Run` with a `Sandbox`, so they check the real runner, not hand-typed commands. Built on mlab (`GOOS=linux GOARCH=amd64 go test -c -tags mserv`), copied to mserv, run there; units are named `wwd-j_4d3-<scenario>` in `wwd.slice`, each cleared by name afterwards. Afterwards: `systemctl --failed` empty, no `wwd-j_4d3*` unit, the test directory and every probe file removed.

## Results

| # | Scenario | Result |
|---|---|---|
| A | basic | unit `0::/wwd.slice/wwd-j_4d3-A.service`, user `wake`, environment is exactly `run.BuildEnv` (HOME, TMPDIR, XDG_*, PDX_*, WWD_JOB, TERM and the PATH the test passed); the daemon's own variable did not reach the job; Exited 0 in 83 ms |
| B | exit code and streams | `exit 3` → Exited 3; stdout and stderr land in different files |
| C | deadline and fuse | worker deadline 3 s → TimedOut in 3.08 s, `Err` nil; `RuntimeMaxSec=3` on a plain unit → systemd says `Result=timeout`, `classify` reads TimedOut / `timeout` |
| D | OOM | `MemoryMax=64M`, a job that takes 300 MB → Exited, Signal `killed`, Reason `oom`, in 111 ms |
| E | docker socket (spec §11) | in the sandbox `docker ps` and `curl --unix-socket /run/docker.sock` both fail; `test -S` still says yes (`InaccessiblePaths` puts an inaccessible socket node over the path: it is there but nothing can connect). Control: the same probe in a unit that does not hide the path reaches docker (`DOCKER_WORKS`, `CURL_WORKS`), so the probe is valid |
| F | writes | the workspace is writable; `$HOME` is not; `/tmp` is private (the job's file is not on the host); `sudo -n true` fails |
| G | `ProtectSystem=strict` | see below |
| H | cancel | TERM ignored by the job: Cancelled after 5.26 s (cancel at 2 s, then the 3 s grace, then KILL); no leftover |
| I | same-name old failed unit | a failed `wwd-j_4d3-I` left behind; the next run of the same id is cleared first and reads its own result (Exited 0) |
| L | task with its own `.wwd-exit` | a task that makes a directory `.wwd-exit` and exits 0 → Exited 0 (the runner's file has another, random name) |
| K | stopped from outside | `systemctl stop` of the running unit by an operator: Cancelled after 1.55 s with `Err` "ended and left no exit status: it was stopped from outside" (systemd-run exits 0 and the unit disappears, so it must not read as a success) |
| J | jobdb on the dev MySQL | **not run**: it needs the administrator account on mserv's MySQL, which is the user's decision |

### G: without and with `ProtectSystem=strict`

| Probe (wake can write these today) | without | with |
|---|---|---|
| `/var/tmp/4d3-x` | wrote | wrote (`PrivateTmp` covers `/var/tmp`: the write is private, not denied) |
| `/var/crash/4d3-x` (group `adm`) | wrote | **denied** |
| `/var/log/installer/4d3-x` (group `adm`) | wrote | **denied** |
| composer install of phpunit 11 into the workspace (empty cache, PHP 8.5.11, composer.phar 2.10.3 fetched and checked against its published sha256) | 6 s, `OK (1 test, 1 assertion)` | 6 s, `OK (1 test, 1 assertion)` |

`/opt` and `/usr/local` hold nothing `wake` owns, so there was nothing to probe there. `find -writable` also shows `/srv/stacks` and `/srv/backups` writable by `wake`; they were deliberately not probed (the second is real backup data), but `ProtectSystem=strict` makes the whole tree read-only by the same mechanism, so a job could not write there with it.
A job's composer cache starts empty (HOME is in the workspace): 6–7 s for this project, not a visible cost.
Not measured: at-inwin's own phpunit suite (it needs the repository and its database).

Decision (lead, 2026-10-18): `ProtectSystem=strict` is in the sandbox's base properties (A–L were run again on mserv with it as a base property: all pass, mserv left clean) (not `ExtraProperties`), and spec §5 lists it, on the strength of this measurement. It closes writes to every path outside the workspace (including `/srv/backups`) and cost nothing that was measured.

## What the real run found (both fixed in this PR; the fake `sudo`/`systemctl` of the unit tests had hidden them)

1. `systemctl reset-failed` on a unit that was never loaded exits 1. The start-up clean-up treated that as "cannot clear" and refused to start every first run. Now it clears only when `LoadState` is not `not-found`.
2. A unit that ended well is unloaded by systemd at once; only a failed one stays to be read. The Result of a successful job is therefore not there, and systemd-run's exit 0 cannot stand for it (a unit stopped from outside also leaves it with 0: scenario K). The command now runs under a one-line shell (`sh -c '"$@"; rc=$?; printf %s "$rc" > "$0" || exit 125; exit "$rc"' <workspace>/.wwd-exit.<16 random hex> <command>`; the name is new for every run, so a task's own `.wwd-exit` or an old file cannot stand for it, and the runner removes it) that leaves its exit status in the workspace: unit gone and systemd-run exit 0 and the file says N → Exited N; no file → stopped from outside (Cancelled with an `Err`, an infrastructure failure for the caller); a non-zero systemd-run exit → nothing was started. A failed unit still stays loaded and its Result is read from systemd as before, with one rule: `exit-code` 125 or 200..243 with no status file is StartFailed (infrastructure), not the task's exit: those are systemd's own set-up failures (scenario M: a `ReadWritePaths` entry that does not exist is 226/NAMESPACE) and the wrapper's exit when it cannot write the file. The runner also refuses a start whose extra writable path is not there. The file separates a finished job from a unit stopped from outside; it is not a boundary against the task itself (a task can read its parent's command line, write the file ahead and so only claim a success it could claim by exiting 0; spec §11: jobs are declared tasks of our own repos). One consequence: a command killed by a signal that the shell survives now reads as exit 128+n instead of a Signal.

## About the per-job database and at-inwin

`Info.WPTestsEnv()` gives the same database under the names `at-inwin-plugin/tests/phpunit/wp-tests-config.php` reads (`WP_TESTS_DB_NAME`, `WP_TESTS_DB_HOST` with the port, `WP_TESTS_DB_USER`, `WP_TESTS_DB_PASSWORD`, table prefix `wptests_`). **Whether a job can use them depends on that file's guard**, which today accepts only `inwin_local` / `100.64.0.6:3307` / `inwin_dev` and exits otherwise; relaxing it is that repository's decision. mserv's dev MySQL is on `:3307`, so `jobdb.Config` needs that Host and Port and a `UserHosts` entry for the address a job connects from.

## Found on the way, not changed here

`TestChildThatIgnoresTermKeepsItsGraceAfterTheLeaderLeft` (`run_test.go`) is unreliable on Linux: it passed once and failed twice in three runs on mserv ("the child survived KILL"), because `alive()` counts a killed but not yet reaped zombie as alive. It is a test problem of the process-group path (macOS), not of the sandbox; mserv jobs use the sandbox.
