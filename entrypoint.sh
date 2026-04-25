#!/bin/sh
set -e

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

validate_puid_pgid() {
    case "$PUID" in
        ''|*[!0-9]*) echo "Error: PUID must be a positive integer, got '$PUID'" >&2; exit 1 ;;
    esac
    case "$PGID" in
        ''|*[!0-9]*) echo "Error: PGID must be a positive integer, got '$PGID'" >&2; exit 1 ;;
    esac
    if [ "$PUID" -eq 0 ]; then
        echo "Error: PUID=0 would run as root — set PUID to your host user UID (e.g. 1000)" >&2
        exit 1
    fi
    if [ "$PGID" -eq 0 ]; then
        echo "Error: PGID=0 would use root group — set PGID to your host group GID (e.g. 1000)" >&2
        exit 1
    fi
}

ensure_group() {
    if getent group appgroup > /dev/null 2>&1; then
        current_gid="$(getent group appgroup | cut -d: -f3)"
        if [ "$current_gid" != "$PGID" ]; then
            groupmod -g "$PGID" appgroup
        fi
    else
        addgroup -g "$PGID" appgroup
    fi
}

ensure_user() {
    if id -u appuser > /dev/null 2>&1; then
        current_uid="$(id -u appuser)"
        current_gid="$(id -g appuser)"
        if [ "$current_uid" != "$PUID" ] || [ "$current_gid" != "$PGID" ]; then
            usermod -u "$PUID" -g "$PGID" appuser
        fi
    else
        adduser -D -u "$PUID" -G appgroup -h /home/appuser -s /sbin/nologin appuser
    fi
}

chown_data_dirs() {
    if [ -d /data ]; then
        current_owner="$(stat -c '%U:%G' /data 2>/dev/null || echo '?')"
        if [ "$current_owner" != "appuser:appgroup" ]; then
            chown -R appuser:appgroup /data
        fi
    fi
}

drop_privileges_and_exec() {
    echo "Running as appuser (UID=$(id -u appuser), GID=$(id -g appuser))"
    exec su-exec appuser:appgroup "$@"
}

validate_puid_pgid
ensure_group
ensure_user
chown_data_dirs
drop_privileges_and_exec "$@"
