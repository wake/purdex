package fleet

// Script is the one command run on a probed host (spec §5). It is a compile-time constant: no byte of it comes from the
// App or the alias. It prints marker sections and ends with @@end, so a cut-off output is told from a complete one. The
// disk mounts in it are the ones DiskMount names for each OS.
const Script = `echo '@@uname'; uname -s
case "$(uname -s)" in
Darwin)
echo '@@ncpu'; sysctl -n hw.ncpu
echo '@@memsize'; sysctl -n hw.memsize
echo '@@load'; sysctl -n vm.loadavg
echo '@@mem'; vm_stat
echo '@@disk'; df -k /System/Volumes/Data 2>/dev/null || df -k /
;;
Linux)
echo '@@ncpu'; nproc
echo '@@load'; cat /proc/loadavg
echo '@@mem'; cat /proc/meminfo
echo '@@disk'; df -kP /
;;
esac
echo '@@end'
`
