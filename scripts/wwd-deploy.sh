#!/bin/sh
# Install or upgrade wwd on one host of the pool (docs/wwd-deploy.md).
#
#   scripts/wwd-deploy.sh [--dry-run] [--no-restart] [--token-file FILE] <mlab|air26|mserv|air19>
#
# Ships the build for that host (bin/wwd-<os>-<arch>, made by `make build-wwd`;
# WWD_BIN_DIR overrides bin/) to the host and runs `wwd install --alias <host>`
# there. mlab is this machine: no copy, no ssh. --token-file is a file on THIS
# machine; it is copied to the host as a 0600 temporary, handed to the install by
# path and removed again. Safe to run again: the install only changes what differs.
set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
bin_dir=${WWD_BIN_DIR:-$script_dir/../bin}

dry=0
passthrough=""
token_file=""
host=""
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) dry=1; passthrough="$passthrough --dry-run" ;;
    --no-restart) passthrough="$passthrough --no-restart" ;;
    --token-file) shift; token_file=${1:-}; [ -n "$token_file" ] || { echo "--token-file needs a file" >&2; exit 2; } ;;
    -*) echo "unknown flag $1" >&2; exit 2 ;;
    *) [ -z "$host" ] || { echo "one host at a time" >&2; exit 2; }; host=$1 ;;
  esac
  shift
done

# host -> build, ssh target
case "$host" in
  mlab)  build=darwin-arm64; target="" ;;
  air26) build=darwin-arm64; target=air26 ;;
  air19) build=darwin-amd64; target=air19 ;;
  mserv) build=linux-amd64;  target=mserv ;;
  *) echo "usage: $0 [--dry-run] [--no-restart] [--token-file FILE] <mlab|air26|mserv|air19>" >&2; exit 2 ;;
esac

binary="$bin_dir/wwd-$build"
[ -f "$binary" ] || { echo "no build at $binary: run \`make build-wwd\`" >&2; exit 1; }
[ -z "$token_file" ] || [ -f "$token_file" ] || { echo "no token file $token_file" >&2; exit 1; }

# the dry run only prints; a real run executes the same lines
do_run() {
  if [ "$dry" -eq 1 ]; then echo "+ $*"; else "$@"; fi
}

remote_dir='~/.local/share/wwd/incoming'
remote_bin="$remote_dir/wwd"
install_args="install --alias $host$passthrough"

if [ -z "$target" ]; then
  # this machine
  tok_arg=""
  [ -z "$token_file" ] || tok_arg=" --token-file $token_file"
  do_run "$binary" $install_args$tok_arg
  exit 0
fi

do_run ssh "$target" "mkdir -p $remote_dir && chmod 700 $remote_dir"
do_run scp -q "$binary" "$target:$remote_bin"
do_run ssh "$target" "chmod 755 $remote_bin"
if [ -n "$token_file" ]; then
  do_run scp -q "$token_file" "$target:$remote_dir/pool.token.new"
  do_run ssh "$target" "chmod 600 $remote_dir/pool.token.new && $remote_bin $install_args --token-file $remote_dir/pool.token.new; rc=\$?; rm -f $remote_dir/pool.token.new; exit \$rc"
else
  do_run ssh "$target" "$remote_bin $install_args"
fi
