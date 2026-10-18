#!/bin/sh
# Runs scripts/wwd-mysql-setup.sh against fake `sudo` and `mysql` commands: what it sends to MySQL, what it writes, what it prints.
set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
setup="$script_dir/wwd-mysql-setup.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $1" >&2; exit 1; }

bin="$tmp/bin"; mkdir -p "$bin"
printf '#!/bin/sh\necho "sudo $*" >> "$LOG/cmds"\nexec "$@"\n' > "$bin/sudo"
# the fake mysql: records its arguments and its standard input; a call with a client file (--defaults-extra-file=F) works when F is there;
# SHOW GRANTS answers the two grants the script makes, plus a global one when FAKE_DRIFT=1 (an account that was given more later)
cat > "$bin/mysql" <<'EOF'
#!/bin/sh
echo "mysql $*" >> "$LOG/cmds"
case "$1" in
  --defaults-extra-file=*) [ -f "${1#--defaults-extra-file=}" ] && exit 0; exit 1 ;;
esac
case "$*" in
  *"SHOW GRANTS"*)
    echo "GRANT USAGE ON *.* TO \`wwd_admin\`@\`localhost\`"
    echo "GRANT EXECUTE ON \`wwdctl\`.* TO \`wwd_admin\`@\`localhost\`"
    [ "${FAKE_DRIFT:-0}" = 1 ] && echo "GRANT CREATE USER ON *.* TO \`wwd_admin\`@\`localhost\`"
    exit 0 ;;
esac
if [ ! -t 0 ]; then cat >> "$LOG/stdin"; fi
exit 0
EOF
chmod +x "$bin/sudo" "$bin/mysql"
LOG="$tmp/log"; mkdir -p "$LOG"; export LOG
df="$tmp/home/.config/wwd/mysql-admin.cnf"
sql="$tmp/wwdctl.sql"; echo "-- fake routines" > "$sql"

run() { PATH="$bin:$PATH" HOME="$tmp/home" sh "$setup" --socket /run/fake.sock --defaults-file "$df" --sql "$sql" "$@"; }
reset() { rm -f "$LOG"/*; rm -rf "$tmp/home"; }

# a dry run sends nothing, writes nothing, and shows no password
reset
out=$(run --dry-run install 2>&1)
[ ! -e "$LOG/cmds" ] || fail "a dry run ran a command: $(cat "$LOG/cmds")"
[ ! -e "$df" ] || fail "a dry run wrote the client file"
echo "$out" | grep -q '<password>' || fail "the dry run does not show where the password goes"
echo "$out" | grep -Eq '[0-9a-f]{48}' && fail "the dry run printed a password"

# install: the routines are loaded as root, the account has EXECUTE on wwdctl and nothing else
reset
out=$(run install 2>&1)
grep -q "sudo mysql -S /run/fake.sock" "$LOG/cmds" || fail "root is not used through sudo and the socket"
grep -q "CREATE USER 'wwd_admin'@'localhost' IDENTIFIED BY '" "$LOG/stdin" || fail "no account was made"
[ "$(grep -c 'GRANT' "$LOG/stdin")" -eq 1 ] || fail "more than one GRANT: $(grep GRANT "$LOG/stdin")"
grep -q "GRANT EXECUTE ON wwdctl.\* TO 'wwd_admin'@'localhost'" "$LOG/stdin" || fail "the one grant is not EXECUTE on wwdctl"
grep -q 'ALL\|\*\.\*\|WITH GRANT' "$LOG/stdin" && fail "a broader privilege was sent"
[ -f "$df" ] || fail "no client file"
[ "$(ls -l "$df" | cut -c1-10)" = "-rw-------" ] || fail "the client file is not 0600: $(ls -l "$df")"
grep -q '^user=wwd_admin$' "$df" || fail "client file has no user"
grep -q '^socket=/run/fake.sock$' "$df" || fail "client file has no socket"
pw=$(sed -n 's/^password=//p' "$df")
[ ${#pw} -eq 48 ] || fail "password is not 48 characters"
grep -q "$pw" "$LOG/stdin" || fail "the password the account got is not the one in the file"
if grep -q "$pw" "$LOG/cmds"; then fail "the password is on a command line"; fi
if echo "$out" | grep -q "$pw"; then fail "the password was printed"; fi

# install again with a client file that still works: the account is left alone
: > "$LOG/stdin"
run install >/dev/null 2>&1
grep -q "CREATE USER" "$LOG/stdin" && fail "a working account was made again"
[ "$(sed -n 's/^password=//p' "$df")" = "$pw" ] || fail "the password changed"

# install again when the account has been given more than it should (grants that drifted): it is made again, with a new password
: > "$LOG/stdin"
FAKE_DRIFT=1 run install >/dev/null 2>&1
grep -q "CREATE USER 'wwd_admin'@'localhost'" "$LOG/stdin" || fail "an account with a drifted grant was kept"
[ "$(sed -n 's/^password=//p' "$df")" != "$pw" ] || fail "the password did not change with the account"
pw=$(sed -n 's/^password=//p' "$df")

# remove: the account and the database wwdctl go, and the client file
: > "$LOG/stdin"
run remove >/dev/null 2>&1
grep -q "DROP USER IF EXISTS 'wwd_admin'@'localhost'" "$LOG/stdin" || fail "the account was not dropped"
grep -q "DROP DATABASE IF EXISTS wwdctl" "$LOG/stdin" || fail "wwdctl was not dropped"
[ ! -e "$df" ] || fail "the client file is still there"

# arguments
rc=0; run frobnicate >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 2 ] || fail "an unknown action must exit 2, got $rc"
rc=0; run >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 2 ] || fail "no action must exit 2, got $rc"
rc=0; PATH="$bin:$PATH" HOME="$tmp/home" sh "$setup" --sql /no/such/file install >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 1 ] || fail "a missing SQL file must exit 1, got $rc"

echo "ok"
