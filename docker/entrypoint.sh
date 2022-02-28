#!/bin/sh

USER_ID=${UID:-99}
GROUP_ID=${GID:-100}

echo "Starting with UID: $USER_ID, GID: $GROUP_ID"
useradd -u "$USER_ID" -o -m user > /dev/null
if [ $(getent group "$GROUP_ID") ]; then
    usermod -g "$GROUP_ID" user > /dev/null
else
    groupmod -g "$GROUP_ID" user > /dev/null
fi
export HOME=/home/user
chown -R "$USER_ID":"$GROUP_ID" /config /input /output

exec runuser -u user -- "$@"