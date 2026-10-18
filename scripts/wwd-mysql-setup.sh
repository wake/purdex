#!/bin/sh
# Install or remove the wwd administrator account on the MySQL dev instance of mserv (docs/wwd-deploy.md, "MySQL for per-job databases").
#
#   scripts/wwd-mysql-setup.sh [--dry-run] [--socket PATH] [--defaults-file FILE] [--sql FILE] install|remove|status
#
# Run it ON mserv, as the user that runs wwd (it needs sudo for the mysql root account, which uses socket authentication).
#   install  loads internal/wwd/jobdb/wwdctl.sql (the database wwdctl and its seven routines, all running as root and checking their
#            arguments), makes the account `wwd_admin`@`localhost` with EXECUTE on wwdctl and nothing else, and writes its client
#            file (0600). Safe to run again: the routines are replaced, and an account whose file still works is left alone.
#   remove   drops the account, the database wwdctl and the client file. Databases and users the pool made for jobs are listed, not
#            dropped (let wwd sweep them first, or drop them by hand).
#   status   shows the grants of the account, the routines and the mode of the client file.
# The password goes to MySQL on standard input and to the client file; it is never an argument and never printed.
set -eu

dry=0
socket=/run/mysqld-dev/mysqld.sock
defaults_file=${HOME:-/root}/.config/wwd/mysql-admin.cnf
here=$(cd "$(dirname "$0")" && pwd)
sql_file="$here/../internal/wwd/jobdb/wwdctl.sql"
action=""
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) dry=1 ;;
    --socket) shift; socket=${1:-} ;;
    --defaults-file) shift; defaults_file=${1:-} ;;
    --sql) shift; sql_file=${1:-} ;;
    install|remove|status) [ -z "$action" ] || { echo "one action at a time" >&2; exit 2; }; action=$1 ;;
    *) echo "unknown argument $1" >&2; exit 2 ;;
  esac
  shift
done
[ -n "$action" ] || { echo "usage: $0 [--dry-run] [--socket PATH] [--defaults-file FILE] [--sql FILE] install|remove|status" >&2; exit 2; }
[ -n "$socket" ] && [ -n "$defaults_file" ] && [ -n "$sql_file" ] || { echo "an option needs a value" >&2; exit 2; }

# root_sql: SQL on standard input to the instance as root. Dry run: shown, not sent.
root_sql() {
  if [ "$dry" -eq 1 ]; then echo "+ sudo mysql -S $socket  <<"; sed 's/^/    /'; else sudo mysql -S "$socket"; fi
}

case "$action" in
install)
  [ -r "$sql_file" ] || { echo "cannot read $sql_file" >&2; exit 1; }
  echo "-- loading $sql_file"
  if [ "$dry" -eq 1 ]; then echo "+ sudo mysql -S $socket < $sql_file"; else sudo mysql -S "$socket" < "$sql_file"; fi
  if [ "$dry" -eq 0 ] && [ -f "$defaults_file" ] && mysql --defaults-extra-file="$defaults_file" -N -B -e 'CALL wwdctl.wwd_list_dbs()' >/dev/null 2>&1; then
    echo "-- the account and its client file $defaults_file work: left as they are"
  else
    echo "-- making the account wwd_admin@localhost and $defaults_file"
    pw=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
    [ ${#pw} -eq 48 ] || { echo "no password could be made" >&2; exit 1; }
    {
      echo "DROP USER IF EXISTS 'wwd_admin'@'localhost';"
      echo "CREATE USER 'wwd_admin'@'localhost' IDENTIFIED BY '$pw';"
      echo "GRANT EXECUTE ON wwdctl.* TO 'wwd_admin'@'localhost';"
    } | { if [ "$dry" -eq 1 ]; then sed "s/$pw/<password>/" | root_sql; else root_sql; fi; }
    if [ "$dry" -eq 1 ]; then
      echo "+ write $defaults_file (0600): [client] socket=$socket user=wwd_admin password=<password>"
    else
      umask 077
      mkdir -p "$(dirname "$defaults_file")"
      chmod 700 "$(dirname "$defaults_file")" 2>/dev/null || true
      printf '[client]\nsocket=%s\nuser=wwd_admin\npassword=%s\n' "$socket" "$pw" > "$defaults_file"
      chmod 600 "$defaults_file"
    fi
  fi
  if [ "$dry" -eq 0 ]; then
    mysql --defaults-extra-file="$defaults_file" -N -B -e 'CALL wwdctl.wwd_list_dbs()' >/dev/null || { echo "the account cannot call wwdctl" >&2; exit 1; }
    echo "-- ok: wwd_admin can call wwdctl"
    sudo mysql -S "$socket" -e "SHOW GRANTS FOR 'wwd_admin'@'localhost'"
  fi
  ;;
remove)
  echo "-- jobs' databases and users that are left (not dropped here):"
  if [ "$dry" -eq 1 ]; then echo "+ CALL wwdctl.wwd_list_dbs(); CALL wwdctl.wwd_list_users();"; else
    sudo mysql -S "$socket" -N -B -e 'CALL wwdctl.wwd_list_dbs(); CALL wwdctl.wwd_list_users()' 2>/dev/null || true
  fi
  printf '%s\n' "DROP USER IF EXISTS 'wwd_admin'@'localhost';" "DROP DATABASE IF EXISTS wwdctl;" | root_sql
  if [ "$dry" -eq 1 ]; then echo "+ rm -f $defaults_file"; else rm -f "$defaults_file"; fi
  ;;
status)
  if [ "$dry" -eq 1 ]; then echo "+ SHOW GRANTS FOR wwd_admin; SHOW PROCEDURE STATUS WHERE Db = 'wwdctl'; ls -l $defaults_file"; exit 0; fi
  sudo mysql -S "$socket" -e "SHOW GRANTS FOR 'wwd_admin'@'localhost'; SHOW PROCEDURE STATUS WHERE Db = 'wwdctl'"
  ls -l "$defaults_file"
  ;;
esac
