#!/bin/sh

# set environment variables for UID and GID
PUID=${UID:-99}
PGID=${GID:-100}

# create a user and group with specified UID and GID
addgroup -g $PGID appgroup
adduser -D -u $PUID -G appgroup appuser

mkdir -p $APP_HOME
chown -R appuser:appuser $APP_HOME

echo "Starting with UID: $PUID, GID: $PGID"

# Fix permissions
chown -R "$PUID":"$PGID" /config /input /output

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

python manage.py collectstatic --noinput

# Start Celery Worker
gosu "$PUID":"$PGID" celery -A bragibooks_proj worker --loglevel=info --concurrency 1 -E &

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
    --enable-stdio-inheritance \

# for debug
#python manage.py runserver 0.0.0.0:8000
