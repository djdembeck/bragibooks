#!/bin/sh

USER_ID=${UID:-99}
GROUP_ID=${GID:-100}

echo "Starting with UID: $USER_ID, GID: $GROUP_ID"
# Fix permissions
chown -R "$USER_ID":"$GROUP_ID" /config /input /output

until cd /home/app/web
do
    echo "Waiting for server volume..."
    sleep 1
done


until gosu "$USER_ID":"$GROUP_ID" python manage.py migrate
do
    echo "Waiting for db to be ready..."
    sleep 2
done

gosu "$USER_ID":"$GROUP_ID"python manage.py collectstatic --noinput

# Start Celery Worker
gosu "$USER_ID":"$GROUP_ID" celery -A bragibooks_proj worker --loglevel=info --concurrency 1 -E &

# If you want to use the admin panel for debugging
# python manage.py createsuperuser --noinput

# Start gunicorn server
gosu "$USER_ID":"$GROUP_ID" gunicorn bragibooks_proj.wsgi \
    --bind 0.0.0.0:8000 \
    --timeout 1200 \
    --worker-tmp-dir /dev/shm \
    --workers 2 \
    --threads 4 \
    --worker-class gthread \
    --enable-stdio-inheritance \

# for debug
#python manage.py runserver 0.0.0.0:8000
