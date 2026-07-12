# =============================================================================
# Runtime stage - Bragibooks with m4b-merge
# =============================================================================
# Base image
FROM ghcr.io/djdembeck/m4b-merge:develop

# Keeps Python from generating .pyc files in the container
ENV PYTHONDONTWRITEBYTECODE=1

# Turns off buffering for easier container logging
ENV PYTHONUNBUFFERED=1

# Setup environment variable
ENV APP_HOME=/home/app/web

# Where your code lives
WORKDIR $APP_HOME

# Install gosu for user switching in entrypoint
# Note: This build step requires internet access to GitHub and keys.openpgp.org
# Install gosu from GitHub releases (not available in Alpine repos)
# Architectures supported by gosu: amd64, arm64, armel, armhf, i386, mips64le, ppc64le, riscv64, s390x
ENV GOSU_VERSION=1.17
RUN set -eux; \
    apk add --no-cache --virtual .gosu-deps \
        ca-certificates \
        dpkg \
        gnupg \
        wget; \
    dpkgArch="$(dpkg --print-architecture | awk -F- '{ print $NF }')"; \
    wget -O /usr/local/bin/gosu "https://github.com/tianon/gosu/releases/download/${GOSU_VERSION}/gosu-${dpkgArch}"; \
    wget -O /usr/local/bin/gosu.asc "https://github.com/tianon/gosu/releases/download/${GOSU_VERSION}/gosu-${dpkgArch}.asc"; \
    export GNUPGHOME="$(mktemp -d)"; \
    gpg --batch --keyserver hkps://keys.openpgp.org --recv-keys B42F6819007F00F88E364FD4036A9C25BF357DD4; \
    gpg --batch --verify /usr/local/bin/gosu.asc /usr/local/bin/gosu; \
    gpgconf --kill gpg-agent; \
    rm -rf "$GNUPGHOME" /usr/local/bin/gosu.asc; \
    chmod +x /usr/local/bin/gosu; \
    gosu --version; \
    gosu nobody true; \
    apk del --no-network .gosu-deps

# Copy the necessary files to the container
COPY requirements.txt .
COPY pyproject.toml .
COPY README.md .

# Run this command to install all dependencies
RUN pip install --no-cache-dir --upgrade pip && \
    pip install --no-cache-dir -r requirements.txt && \
    pip install --no-cache-dir -e .

# Copy whole project to your docker home directory.
COPY bragibooks_proj $APP_HOME/bragibooks_proj/
COPY importer $APP_HOME/importer/
COPY utils $APP_HOME/utils/
COPY manage.py $APP_HOME

# Copy entrypoint file
COPY docker/entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/entrypoint.sh

# Port where the Django app runs
EXPOSE 8000

# Entrypoint for the container
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
