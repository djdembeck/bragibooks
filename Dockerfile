FROM alpine:3.10 AS ffbase

RUN buildDeps="autoconf \
    automake \
    bash \
    binutils \
    build-base \
    bzip2 \
    cmake \
    curl \
    coreutils \
    diffutils \
    expat-dev \
    file \
    findutils \
    g++ \
    gperf \
    libarchive-tools \
    libgomp \
    libtool \
    nasm \
    python3 \
    openssl \
    openssl-dev \
    tar \
    util-linux-dev \
    yasm \
    zlib-dev" && \
    apk add --no-cache ${buildDeps}

FROM ffbase AS ffbuild

WORKDIR /tmp/workdir

ARG PKG_CONFIG_PATH=/opt/ffmpeg/lib/pkgconfig
ARG LD_LIBRARY_PATH=/opt/ffmpeg/lib
ARG PREFIX=/opt/ffmpeg
ARG MAKEFLAGS="-j$(nproc)"

ENV FFMPEG_VERSION=snapshot \
    LAME_VERSION=3.100 \
    OPUS_VERSION=1.3.1 \
    SRC=/usr/local

ARG OPUS_SHA256SUM="65b58e1e25b2a114157014736a3d9dfeaad8d41be1c8179866f144a2fb44ff9d opus-${OPUS_VERSION}.tar.gz"

### fdk-aac https://github.com/mstorsjo/fdk-aac
RUN \
        DIR=/tmp/fdk-aac && \
        mkdir -p ${DIR} && \
        cd ${DIR} && \
        curl -sL https://github.com/mstorsjo/fdk-aac/archive/master.zip -o fdk-aac-master.zip && \
        bsdtar --strip-components=1 -xf fdk-aac-master.zip && \
        rm fdk-aac-master.zip && \
        autoreconf -fiv && \
        ./configure --prefix="${PREFIX}" --disable-shared --datadir="${DIR}" && \
        make && \
        make install

### libopus https://www.opus-codec.org/
RUN \
        DIR=/tmp/opus && \
        mkdir -p ${DIR} && \
        cd ${DIR} && \
        curl -sLO https://archive.mozilla.org/pub/opus/opus-${OPUS_VERSION}.tar.gz && \
        echo ${OPUS_SHA256SUM} | sha256sum --check && \
        tar -zx --strip-components=1 -f opus-${OPUS_VERSION}.tar.gz && \
        autoreconf -fiv && \
        ./configure --prefix="${PREFIX}" --disable-shared && \
        make && \
        make install
        
### libmp3lame http://lame.sourceforge.net/
RUN \
        DIR=/tmp/lame && \
        mkdir -p ${DIR} && \
        cd ${DIR} && \
        curl -sL https://versaweb.dl.sourceforge.net/project/lame/lame/$(echo ${LAME_VERSION} | sed -e 's/[^0-9]*\([0-9]*\)[.]\([0-9]*\)[.]\([0-9]*\)\([0-9A-Za-z-]*\)/\1.\2/')/lame-${LAME_VERSION}.tar.gz | \
        tar -zx --strip-components=1 && \
        ./configure --prefix="${PREFIX}" --bindir="${PREFIX}/bin" --disable-shared --enable-nasm --enable-pic --disable-frontend && \
        make && \
        make install

## ffmpeg https://ffmpeg.org/
RUN  \
        DIR=/tmp/ffmpeg && mkdir -p ${DIR} && cd ${DIR} && \
        curl -LO https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.bz2 && \
        tar -jx --strip-components=1 -f ffmpeg-${FFMPEG_VERSION}.tar.bz2

RUN \
        DIR=/tmp/ffmpeg && mkdir -p ${DIR} && cd ${DIR} && \
        ./configure \
        --enable-ffplay \
        --enable-gpl \
        --enable-version3 \
        --enable-libfdk-aac \
        --enable-libmp3lame \
        --enable-libopus \
        --enable-nonfree \
        --enable-openssl \
        --pkg-config-flags="--static" \
        --extra-cflags="-I$PREFIX/include" \
        --extra-ldflags="-L$PREFIX/lib" \
        --extra-libs="-lpthread -lm -lz" \
        --extra-ldexeflags="-static" \
        --prefix="${PREFIX}" && \
        make && \
        make install

# base image
FROM python:3.8

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
    rm -rf /var/lib/apt/lists/*

RUN \
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
    apt-get remove -y wget && \
    mkdir -p $DockerHOME

# where your code lives  
WORKDIR $DockerHOME

# set environment variables
ENV PYTHONDONTWRITEBYTECODE 1
ENV PYTHONUNBUFFERED 1

# install dependencies
RUN pip install --no-cache-dir --upgrade pip

# copy whole project to your docker home directory.
COPY . $DockerHOME

# run this command to install all dependencies
RUN pip install --no-cache-dir -r https://raw.githubusercontent.com/djdembeck/m4b-merge/main/requirements.txt \
    pip install --no-cache-dir https://codeload.github.com/djdembeck/m4b-merge/zip/refs/heads/main \
    pip install -r requirements.txt

# port where the Django app runs
EXPOSE 8000

# RUN useradd -rm -d /app -m -s /bin/bash -g 100 -u 99 abc

COPY --from=ffbuild /opt/ffmpeg/bin/ffmpeg /usr/bin
COPY --from=ffbuild /opt/ffmpeg/bin/ffprobe /usr/bin

# USER 99:100

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