# =============================================================================
# Build stage - Compile the Rust m4b-merge binary
# Using Alpine for musl-based static linking (compatible with Alpine runtime)
# =============================================================================
FROM rust:1.93-alpine AS m4b-merge-builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache \
    build-base \
    pkgconfig \
    openssl-dev \
    openssl-libs-static \
    git

# Build arg for m4b-merge branch/tag
ARG M4B_MERGE_REF=develop

# Clone m4b-merge from source
RUN git clone --depth 1 --branch ${M4B_MERGE_REF} https://github.com/djdembeck/m4b-merge.git /build/m4b-merge

WORKDIR /build/m4b-merge

# Build a statically linked binary for Alpine compatibility
ENV RUSTFLAGS="-C target-feature=+crt-static"
RUN cargo build --release --locked --target x86_64-unknown-linux-musl

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

# Copy the necessary files to the container
COPY requirements.txt .

# Run this command to install all dependencies
RUN pip install --no-cache-dir --upgrade pip && \
    pip install --no-cache-dir -r requirements.txt

# Copy the statically linked Rust m4b-merge binary from builder stage
COPY --from=m4b-merge-builder /build/m4b-merge/target/x86_64-unknown-linux-musl/release/m4b-merge /usr/local/bin/m4b-merge
RUN chmod +x /usr/local/bin/m4b-merge

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
