# base image
FROM ghcr.io/djdembeck/m4b-merge:develop

# Keeps Python from generating .pyc files in the container
ENV PYTHONDONTWRITEBYTECODE=1

# Turns off buffering for easier container logging
ENV PYTHONUNBUFFERED=1

# setup environment variable
ENV DockerHOME=/home/app/web

# where your code lives  
WORKDIR $DockerHOME

# copy whole project to your docker home directory.
COPY . $DockerHOME

# run this command to install all dependencies
RUN pip install --no-cache-dir --upgrade pip && \
    pip install --no-cache-dir -r requirements.txt

# port where the Django app runs
EXPOSE 8000

ENTRYPOINT ["/bin/sh", "docker/entrypoint.sh"]
