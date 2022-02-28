# base image
FROM ghcr.io/djdembeck/m4b-merge:develop

# setup environment variable
ENV DockerHOME=/home/app/web

# where your code lives  
WORKDIR $DockerHOME

# copy whole project to your docker home directory.
COPY . $DockerHOME
COPY ./importer/static static

# run this command to install all dependencies
RUN pip install --no-cache-dir -r requirements.txt

# port where the Django app runs
EXPOSE 8000

ENTRYPOINT ["/bin/sh", "docker/entrypoint.sh"]

# start server
CMD python manage.py migrate && \
    gunicorn bragibooks_proj.wsgi \
    --bind 0.0.0.0:8000 \
    --timeout 1200 \
    --worker-tmp-dir /dev/shm \
    --workers=2 \
    --threads=4 \
    --worker-class=gthread \
    --reload \
    --enable-stdio-inheritance