#!/bin/sh

USER_ID=${UID:-99}
GROUP_ID=${GID:-100}

echo "Starting with UID: $USER_ID, GID: $GROUP_ID"
# Fix permissions
chown -R "$USER_ID":"$GROUP_ID" /config /input /output

# Run the command as the specified user
gosu "$USER_ID":"$GROUP_ID" "$@"