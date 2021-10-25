#!/bin/sh

chown -R appuser:appgroup /config /input /output
exec runuser -u worker "$@"