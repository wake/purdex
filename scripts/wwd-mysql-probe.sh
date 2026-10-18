#!/bin/sh
# Checks, on mserv, what the wwd administrator account may and may not do on the MySQL dev instance (docs/wwd-deploy.md). Run it
# after `wwd-mysql-setup.sh install`; it needs the client file ~/.config/wwd/mysql-admin.cnf and no sudo. It makes one database and
# one user of the right shape (wwd_00000000000000000000000000ffffff / wwdu_00000000000000000000000000), and drops them again.
#
#   sh wwd-mysql-probe.sh [--defaults-file FILE] [--socket PATH]
#
# Exit 0 only when every line says OK.
set -u

df=${HOME:-/root}/.config/wwd/mysql-admin.cnf
socket=/run/mysqld-dev/mysqld.sock
while [ $# -gt 0 ]; do
  case "$1" in
    --defaults-file) shift; df=${1:-} ;;
    --socket) shift; socket=${1:-} ;;
    *) echo "unknown argument $1" >&2; exit 2 ;;
  esac
  shift
done

bad=0
ok() { printf 'OK   %s\n' "$1"; }
fail() { printf 'FAIL %s\n' "$1"; bad=1; }
adm() { mysql --defaults-extra-file="$df" -N -B "$@" 2>/dev/null; }
DB=wwd_00000000000000000000000000ffffff
USER=wwdu_00000000000000000000000000
PW=0123456789abcdef0123456789abcdef0123456789abcdef
job() { MYSQL_PWD="$PW" mysql -N -B -u "$USER" -S "$socket" "$@" 2>/dev/null; }

# ---- what the administrator account must NOT be able to do --------------------------------------------------------------------
refused() {
  if adm -e "$1" >/dev/null; then fail "was allowed: $2"; else ok "refused: $2"; fi
}
refused "CREATE DATABASE wwd_evil" "CREATE DATABASE"
refused "DROP DATABASE inwin_local" "DROP DATABASE of another project"
refused "SELECT user FROM mysql.user" "read mysql.user"
refused "SELECT 1 FROM inwin_local.wp_options LIMIT 1" "read another project's table"
refused "CREATE USER 'x'@'localhost'" "CREATE USER"
refused "DROP USER 'inwin_dev'@'100.64.0.2'" "DROP USER of another project"
refused "KILL 1" "KILL"
refused "CREATE PROCEDURE wwdctl.x() BEGIN END" "CREATE PROCEDURE in wwdctl"
refused "CALL wwdctl.wwd_create_db('inwin_local')" "wwd_create_db of a name that is not ours"
refused "CALL wwdctl.wwd_drop_db('inwin_local')" "wwd_drop_db of another project's database"
refused "CALL wwdctl.wwd_drop_db('wwd_0123456789abcdef0123456789abcdeF')" "an upper-case hex digit"
refused "CALL wwdctl.wwd_drop_db('wwd_0123456789abcdef0123456789abcdef
')" "a final newline (the end anchor is \\z, not \$)"
refused "CALL wwdctl.wwd_drop_db('wwd_0123456789abcdef0123456789abcdef\`; DROP DATABASE inwin_local; --')" "a quoted injection in the database name"
refused "CALL wwdctl.wwd_drop_user('inwin_dev','100.64.0.2')" "wwd_drop_user of another project's user"
refused "CALL wwdctl.wwd_drop_user('$USER','%')" "a wildcard host"
refused "CALL wwdctl.wwd_kill_user('root')" "wwd_kill_user of root"
refused "CALL wwdctl.wwd_create_user('$USER','$PW','localhost','wwd_ffffffffffffffffffffffffffffffff')" "a user for a database that is not its own"
refused "CALL wwdctl.wwd_create_user('$USER','$PW','localhost''; DROP DATABASE inwin_local; --','$DB')" "a quoted injection in the host"
refused "CALL wwdctl.wwd_create_user('root','$PW','localhost','$DB')" "a user that is not a wwd user"
refused "CALL wwdctl.wwd_create_user('$USER','not-hex','localhost','$DB')" "a password that is not 48 hex"

# SHOW DATABASES is allowed to everyone, but shows only what the account has a privilege on
dbs=$(adm -e "SHOW DATABASES" | tr '\n' ' ')
[ "$dbs" = "information_schema performance_schema wwdctl " ] && ok "the administrator sees only wwdctl (and the two system views)" || fail "the administrator sees: $dbs"

# ---- what it must be able to do: a whole life of a job's database ----------------------------------------------------------------
adm -e "CALL wwdctl.wwd_create_db('$DB'); CALL wwdctl.wwd_create_user('$USER','$PW','localhost','$DB')" && ok "create a database and its user" || fail "create"
adm -e "CALL wwdctl.wwd_list_dbs()" | grep -q "$DB" && ok "the database is listed" || fail "the database is not listed"
adm -e "CALL wwdctl.wwd_list_users()" | grep -q "$USER" && ok "the user is listed" || fail "the user is not listed"

# the job's user: its own database yes, nothing else
job -e "CREATE TABLE $DB.t (a INT); INSERT INTO $DB.t VALUES (1); SELECT a FROM $DB.t" | grep -q 1 && ok "the job's user works in its own database" || fail "the job's user cannot use its database"
seen=$(job -e "SHOW DATABASES" | tr '\n' ' ')
[ "$seen" = "information_schema performance_schema $DB " ] && ok "the job's user sees its database and the two system views only" || fail "the job's user sees: $seen"
seen=$(job -e "SELECT schema_name FROM information_schema.schemata" | tr '\n' ' ')
[ "$seen" = "information_schema performance_schema $DB " ] && ok "information_schema does not list other projects' databases" || fail "information_schema shows: $seen"
for sql in "SELECT 1 FROM inwin_local.wp_options LIMIT 1" "SELECT user FROM mysql.user" "CREATE DATABASE wwd_evil" "CREATE USER 'z'@'localhost'" "GRANT ALL ON $DB.* TO 'root'@'localhost'"; do
  if job -e "$sql" >/dev/null; then fail "the job's user could: $sql"; else ok "the job's user is refused: $sql"; fi
done

# a connection that is held open is ended by wwd_kill_user, and the routine says none is left
( job -e "SELECT SLEEP(20)" >/dev/null ) &
sleep 1
left=$(adm -e "CALL wwdctl.wwd_kill_user('$USER')")
[ "$left" = 0 ] && ok "wwd_kill_user ended the open connection (remaining 0)" || fail "wwd_kill_user left: $left"
wait

adm -e "CALL wwdctl.wwd_drop_user('$USER','localhost'); CALL wwdctl.wwd_drop_db('$DB')" && ok "drop the user and the database" || fail "drop"
if [ -z "$(adm -e 'CALL wwdctl.wwd_list_dbs()')" ] && [ -z "$(adm -e 'CALL wwdctl.wwd_list_users()')" ]; then ok "nothing is left"; else fail "something is left"; fi

[ "$bad" -eq 0 ] && echo "ALL OK" || echo "SOMETHING FAILED"
exit "$bad"
