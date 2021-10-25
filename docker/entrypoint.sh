#!/bin/sh

chown -R worker:worker /config /input /output
exec runuser -u worker -- "$@"