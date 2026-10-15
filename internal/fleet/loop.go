package fleet

import "fmt"

// Frame markers of the loop (spec §5). A frame runs from FrameStart to FrameEnd; every fifth also carries the slow part
// (@@s … the process lists and the gate section) before its @@end.
const (
	FrameStart = "@@f"
	FrameEnd   = "@@end"
)

// SlowEvery is how many fast frames pass between two slow parts (spec §2.2: 1 s and 5 s).
const SlowEvery = 5

// gateShare is how many bytes of `pdx gate status --json` the loop passes on: the rest of a 64 KiB frame stays for the five
// fast items and the process lists. A cut-off JSON does not parse and only blanks that host's queue.
const gateShare = "32768"

// loopBody is the loop, run by `sh -c` on the remote host (so the remote login shell, whatever it is, only sees
// `exec sh -c '…'`). It is a compile-time constant: no byte of it comes from the App or the alias, and it has no single quote
// (it is wrapped in them) and no backslash other than the \n of printf formats. It reads only; it leaves no file.
//
// Darwin is clocked by one long-running `iostat -n0 -w1` (real CPU use and the load, no fork per second for them); Linux by
// `sleep 1` with /proc read by shell built-ins. The loop ends when a write fails, and a trap takes the children along.
const loopBody = `LC_ALL=C; export LC_ALL
trap "trap - HUP INT TERM EXIT; kill 0" HUP INT TERM EXIT
os=$(uname -s)
pdxbin=
for c in "$HOME/.local/bin/pdx" "$HOME/.config/pdx/bin/pdx"; do
 if [ -x "$c" ]; then pdxbin=$c; break; fi
done
p() { printf "$@" || exit; }
slow() {
 if [ "$os" = Darwin ]; then pl=$(ps -Aceo pid=,pcpu=,rss=,comm=); else pl=$(ps -eo pid=,pcpu=,rss=,comm=); fi
 p "@@s\n@@top_cpu\n"; printf "%s\n" "$pl" | sort -k2,2nr | head -n 5
 p "@@top_mem\n"; printf "%s\n" "$pl" | sort -k3,3nr | head -n 5
 if [ -n "$pdxbin" ]; then
  p "@@gate\n"; "$pdxbin" gate status --json 2>/dev/null | head -c ` + gateShare + `; p "\n@@gate_end\n"
 else
  p "@@gate none\n"
 fi
}
darwin() {
 ncpu=$(sysctl -n hw.ncpu); msz=$(sysctl -n hw.memsize); cnt=0; first=1
 iostat -n0 -w1 | while read -r us sy id l1 rest; do
  case $us in ""|*[!0-9]*) continue;; esac
  if [ $first = 1 ]; then first=0; continue; fi
  lv=$(sysctl -n kern.memorystatus_vm_pressure_level)
  vm=$(vm_stat)
  dk=$(df -k /System/Volumes/Data 2>/dev/null || df -k /)
  p "@@f\n@@os\nDarwin\n@@ncpu\n%s\n@@memsize\n%s\n@@load\n%s\n@@cpu\n%s %s %s\n@@level\n%s\n@@vm\n%s\n@@disk\n%s\n" "$ncpu" "$msz" "$l1" "$us" "$sy" "$id" "$lv" "$vm" "$dk"
  if [ $((cnt % 5)) -eq 0 ]; then slow; fi
  cnt=$((cnt + 1))
  p "@@end\n"
 done
}
linux() {
 ncpu=$(nproc); cnt=0
 read -r _ cu cn cs ci cw cq cx ct _ < /proc/stat
 pu=$((cu + cn)); ps_=$((cs + cq + cx + ct)); pi=$((ci + cw))
 while :; do
  sleep 1 || exit
  read -r _ cu cn cs ci cw cq cx ct _ < /proc/stat
  u=$((cu + cn)); s=$((cs + cq + cx + ct)); i=$((ci + cw))
  dt=$((u - pu + s - ps_ + i - pi)); us=0; sy=0; id=100
  if [ "$dt" -gt 0 ]; then us=$((100 * (u - pu) / dt)); sy=$((100 * (s - ps_) / dt)); id=$((100 - us - sy)); fi
  pu=$u; ps_=$s; pi=$i
  read -r l1 _ < /proc/loadavg
  p "@@f\n@@os\nLinux\n@@ncpu\n%s\n@@load\n%s\n@@cpu\n%s %s %s\n@@level\n" "$ncpu" "$l1" "$us" "$sy" "$id"
  if [ -r /proc/pressure/memory ]; then
   while read -r line; do p "%s\n" "$line"; done < /proc/pressure/memory
  fi
  p "@@vm\n"
  while read -r k v _; do
   case $k in MemTotal:|MemAvailable:) p "%s %s\n" "$k" "$v";; esac
  done < /proc/meminfo
  while read -r k v; do
   case $k in pswpin|pswpout) p "%s %s\n" "$k" "$v";; esac
  done < /proc/vmstat
  p "@@disk\n"; df -kP /
  if [ $((cnt % 5)) -eq 0 ]; then slow; fi
  cnt=$((cnt + 1))
  p "@@end\n"
 done
}
case $os in
Darwin) darwin;;
Linux) linux;;
*) p "@@f\n@@os\n%s\n@@end\n" "$os";;
esac
`

// Loop is the remote command: the loop body under `sh -c`, whatever the remote login shell is.
const Loop = "exec sh -c '" + loopBody + "'"

// LoopArgs is the whole argv after the program name for the long-lived session (spec §5): the options first, then `--` so the
// alias can never be read as an option, then the constant loop. Nothing goes through a local shell. An alias of the wrong
// shape is refused.
func LoopArgs(alias string) ([]string, error) {
	if !aliasRE.MatchString(alias) {
		return nil, fmt.Errorf("fleet: bad ssh alias %q", alias)
	}
	return []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "RemoteCommand=none",
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=2",
		"--", alias, Loop,
	}, nil
}
