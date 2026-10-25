#!/bin/sh
# Container entrypoint.
#
# Runs as root by default (matching the legacy image) and drops to the
# requested identity via setpriv, so host-mounted volumes stay writable.
# Identity comes from UID/GID (legacy variable names, same defaults as the
# pre-rewrite image: 99/100); PUID/PGID are accepted as aliases.
set -eu

PUID="${UID:-${PUID:-99}}"
PGID="${GID:-${PGID:-100}}"

if [ "$(id -u)" = "0" ]; then
    chown -R "${PUID}:${PGID}" /config /input /output || \
        echo "warning: could not chown runtime directories; continuing" >&2
    exec setpriv --reuid "${PUID}" --regid "${PGID}" --clear-groups /usr/local/bin/bragibooks "$@"
fi

exec /usr/local/bin/bragibooks "$@"
