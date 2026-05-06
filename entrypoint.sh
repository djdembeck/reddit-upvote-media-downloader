#!/bin/sh
# Entrypoint script for Reddit Media Downloader container
# Handles dynamic UID/GID configuration and privilege dropping
#
# Environment variables:
#   PUID  - User ID to run as (default: 1000)
#   PGID  - Group ID to run as (default: 1000)
#
# Exit codes:
#   1 - Invalid PUID/PGID configuration
#   Propagated - groupmod, usermod, chown, or su-exec failures (via set -e)
#
# Note: This script uses a main-guard pattern to support sourcing for tests.
#       When sourced, only functions are defined; main execution is skipped.
set -e

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

validate_id() {
    name=$1
    value=$2
    id_type=$3
    case "$value" in
        ''|*[!0-9]*) echo "Error: $name must be a positive integer, got '$value'" >&2; exit 1 ;;
    esac
    if [ "$value" -eq 0 ]; then
        echo "Error: $name=0 would run as root — set $name to your host $id_type (e.g. 1000)" >&2
        exit 1
    fi
}

validate_puid_pgid() {
    validate_id PUID "$PUID" UID
    validate_id PGID "$PGID" GID
}

# ensure_group creates or updates the appgroup with the specified PGID.
# If the group exists with a different GID, it is updated.
# If the group doesn't exist, it is created.
ensure_group() {
    if getent group appgroup > /dev/null 2>&1; then
        current_gid="$(getent group appgroup | cut -d: -f3)"
        if [ "$current_gid" != "$PGID" ]; then
            groupmod -g "$PGID" -o appgroup
        fi
    else
        addgroup -g "$PGID" appgroup
    fi
}

# ensure_user creates or updates the appuser with the specified PUID and PGID.
# If the user exists with different UID/GID, it is updated.
# If the user doesn't exist, it is created with appgroup as primary group.
ensure_user() {
    if id -u appuser > /dev/null 2>&1; then
        current_uid="$(id -u appuser)"
        current_gid="$(id -g appuser)"
        if [ "$current_uid" != "$PUID" ] || [ "$current_gid" != "$PGID" ]; then
            usermod -u "$PUID" -g "$PGID" -o appuser
        fi
    else
        adduser -D -u "$PUID" -G appgroup -h /home/appuser -s /sbin/nologin appuser
    fi
}

chown_app_dirs() {
    expected_numeric="${PUID}:${PGID}"
    for dir in "$OUTPUT_DIR" "$(dirname "$DB_PATH")" /data; do
        [ -z "$dir" ] || [ "$dir" = "." ] && continue
        [ -d "$dir" ] || continue
        current_numeric="$(stat -c '%u:%g' "$dir" 2>/dev/null || echo '?:?')"
        if [ "$current_numeric" != "$expected_numeric" ]; then
            chown -R "${PUID}:${PGID}" "$dir"
        fi
    done
}

# drop_privileges_and_exec switches to appuser:appgroup and executes the provided command.
# Uses su-exec to run the command with reduced privileges.
# Replaces the current shell process with the target command (exec).
drop_privileges_and_exec() {
    echo "Running as appuser (UID=$(id -u appuser), GID=$(id -g appuser))"
    exec su-exec appuser:appgroup "$@"
}

# Main execution - only run when script is executed directly, not when sourced
# This guard enables test files to source functions without triggering execution
if [ "${0##*/}" = "entrypoint.sh" ]; then
    validate_puid_pgid
    ensure_group
    ensure_user
    chown_app_dirs
    drop_privileges_and_exec "$@"
fi
