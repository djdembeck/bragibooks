# base image
FROM ghcr.io/djdembeck/m4b-merge/m4b-merge:main

# setup environment variable
ENV DockerHOME=/home/app/webapp

RUN \
    touch /etc/apt/sources.list.d/contrib.list && \
    echo "deb http://ftp.us.debian.org/debian buster main contrib non-free" >> /etc/apt/sources.list.d/contrib.list 

# Get dependencies for m4b-tool/ffmpeg
RUN	apt-get update && \
    apt-get install --no-install-recommends -y \
    fdkaac \
    php-cli \
    php-common \
    php-intl \
    php-mbstring \
    php-xml \
    wget && \
    M4B_TOOL_PRE_RELEASE_LINK="$(wget -nv -O - https://github.com/sandreas/m4b-tool/releases/tag/latest | grep -o 'M4B_TOOL_DOWNLOAD_LINK=[^ ]*' | head -1 | cut -d '=' -f 2)" && \
    wget --progress=dot:giga "$M4B_TOOL_PRE_RELEASE_LINK" -O /tmp/m4b-tool.tar.gz && \
    tar -xf /tmp/m4b-tool.tar.gz -C /tmp && \
    rm /tmp/m4b-tool.tar.gz && \
    mv /tmp/m4b-tool.phar /usr/local/bin/m4b-tool && \
    chmod +x /usr/local/bin/m4b-tool && \
    wget --progress=dot:giga http://archive.ubuntu.com/ubuntu/pool/universe/m/mp4v2/libmp4v2-2_2.0.0~dfsg0-6_amd64.deb && \
    wget --progress=dot:giga http://archive.ubuntu.com/ubuntu/pool/universe/m/mp4v2/mp4v2-utils_2.0.0~dfsg0-6_amd64.deb && \
    dpkg -i libmp4v2-2_2.0.0~dfsg0-6_amd64.deb && \
    dpkg -i mp4v2-utils_2.0.0~dfsg0-6_amd64.deb && \
    rm ./*.deb && \
    rm -rf /var/lib/apt/lists/* && \
    apt-get remove -y wget && \
    mkdir -p $DockerHOME && \
    adduser worker && \
    chown -R worker $DockerHOME

USER worker

# where your code lives  
WORKDIR $DockerHOME

# set environment variables
ENV PYTHONDONTWRITEBYTECODE 1
ENV PYTHONUNBUFFERED 1
ENV PATH="/home/worker/.local/bin:${PATH}"

# install dependencies
RUN pip install --user --no-cache-dir --upgrade pip

# copy whole project to your docker home directory.
COPY . $DockerHOME

# run this command to install all dependencies
RUN pip install --user --no-cache-dir -r https://raw.githubusercontent.com/djdembeck/m4b-merge/main/requirements.txt \
    pip install --user --no-cache-dir https://codeload.github.com/djdembeck/m4b-merge/zip/refs/heads/main \
    pip install --user -r requirements.txt && \
    python manage.py collectstatic

# port where the Django app runs
EXPOSE 8000

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