#!/bin/sh
# Minimal container entrypoint.
#
# The image runs as `app` (uid/gid 1000) by default, so the plain case just
# execs the server. When the container is started as root (e.g.
# `docker run --user root`), the runtime directories are chowned to
# PUID/PGID and the server is re-executed as that user via setpriv, so
# host-mounted volumes remain writable.
set -eu

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

if [ "$(id -u)" = "0" ]; then
    chown -R "${PUID}:${PGID}" /config /input /output || \
        echo "warning: could not chown runtime directories; continuing" >&2
    exec setpriv --reuid "${PUID}" --regid "${PGID}" --clear-groups /usr/local/bin/bragibooks "$@"
fi

exec /usr/local/bin/bragibooks "$@"
