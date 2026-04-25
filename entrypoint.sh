#!/bin/sh
set -e

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

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
        chown -R appuser:appgroup /data
    fi
}

drop_privileges_and_exec() {
    echo "Running as appuser (UID=$(id -u appuser), GID=$(id -g appuser))"
    exec su-exec appuser:appgroup "$@"
}

ensure_group
ensure_user
chown_data_dirs
drop_privileges_and_exec "$@"
