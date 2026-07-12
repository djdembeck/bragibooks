#!/bin/sh

# set environment variables for UID and GID
PUID=${UID:-99}
PGID=${GID:-100}

EXISTING_GROUP=$(getent group "$PGID" | cut -d: -f1)
if [ -n "$EXISTING_GROUP" ]; then
    echo "Group with GID $PGID already exists: $EXISTING_GROUP"
    GROUP_NAME="$EXISTING_GROUP"
else
    addgroup -g "$PGID" appgroup
    GROUP_NAME="appgroup"
fi

EXISTING_USER=$(getent passwd "$PUID" | cut -d: -f1)
if [ -n "$EXISTING_USER" ]; then
    echo "User with UID $PUID already exists: $EXISTING_USER"
    USER_NAME="$EXISTING_USER"
else
    adduser -D -u "$PUID" -G "$GROUP_NAME" appuser
    USER_NAME="appuser"
fi

mkdir -p "$APP_HOME"
if find "$APP_HOME" ! -user "$PUID" -o ! -group "$PGID" | head -n 1 | grep -q .; then
    chown -R "$USER_NAME":"$GROUP_NAME" "$APP_HOME"
fi

echo "Starting with UID: $PUID, GID: $PGID (user: $USER_NAME, group: $GROUP_NAME)"

# Fix permissions
if find /config /input /output ! -user "$PUID" -o ! -group "$PGID" | head -n 1 | grep -q .; then
    chown -R "$USER_NAME":"$GROUP_NAME" /config /input /output
fi

until cd /home/app/web
do
    echo "Waiting for server volume..."
    sleep 1
done

until python manage.py migrate
do
    echo "Waiting for db to be ready..."
    sleep 2
done

gosu "$PUID":"$PGID" python manage.py collectstatic --noinput

# Start Celery Worker
gosu "$PUID":"$PGID" celery -A bragibooks_proj worker --loglevel=info --concurrency ${CELERY_WORKERS:-1} -E &

# If you want to use the admin panel for debugging
# python manage.py createsuperuser --noinput

# Start gunicorn server
gosu "$PUID":"$PGID" gunicorn bragibooks_proj.wsgi \
    --bind 0.0.0.0:8000 \
    --timeout 1200 \
    --worker-tmp-dir /dev/shm \
    --workers 2 \
    --threads 4 \
    --worker-class gthread \
    --enable-stdio-inheritance

# for debug
#python manage.py runserver 0.0.0.0:8000
