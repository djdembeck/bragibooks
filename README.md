<p align="center">
  <a href="" rel="noopener">
 <img width=200px height=200px src="../assets/logos/logo.png?raw=true" alt="Project logo"></a>
</p>

<h3 align="center">Bragibooks</h3>

<div align="center">

[![Status](https://img.shields.io/badge/status-active-success.svg)]()
[![GitHub Issues](https://img.shields.io/github/issues/djdembeck/bragibooks.svg)](https://github.com/djdembeck/bragibooks/issues)
[![GitHub Pull Requests](https://img.shields.io/github/issues-pr/djdembeck/bragibooks.svg)](https://github.com/djdembeck/bragibooks/pulls)
[![License](https://img.shields.io/github/license/djdembeck/bragibooks)](https://github.com/djdembeck/bragibooks/blob/develop/LICENSE)
[![Docker](https://github.com/djdembeck/bragibooks/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/djdembeck/bragibooks/actions/workflows/docker-publish.yml)
[![Docker Pulls](https://img.shields.io/docker/pulls/djdembeck/bragibooks)](https://hub.docker.com/r/djdembeck/bragibooks)
[![Docker Image Size (latest by date)](https://img.shields.io/docker/image-size/djdembeck/bragibooks)](https://hub.docker.com/r/djdembeck/bragibooks)
[![Docker Image Version (latest by date)](https://img.shields.io/docker/v/djdembeck/bragibooks)](https://hub.docker.com/r/djdembeck/bragibooks)
[![CodeFactor Grade](https://img.shields.io/codefactor/grade/github/djdembeck/bragibooks)](https://www.codefactor.io/repository/github/djdembeck/bragibooks)
<!-- ALL-CONTRIBUTORS-BADGE:START - Do not remove or modify this section -->
[![All Contributors](https://img.shields.io/badge/all_contributors-2-orange.svg?style=flat-square)](#contributors-)
<!-- ALL-CONTRIBUTORS-BADGE:END -->

</div>

---

<p align="center"> An audiobook library cleanup & management app, written as a frontend for web use of <a href="https://github.com/djdembeck/m4b-merge">m4b-merge</a>.
    <br> 
</p>

## 📝 Table of Contents

- [About](#about)
- [Getting Started](#getting_started)
- [Usage](#usage)
- [Environment Variables](#env_vars)
- [Built Using](#built_using)
- [Changelog](CHANGELOG.md)
- [Contributing](../CONTRIBUTING.md)
- [Authors](#authors)
- [Contributors](#contributors)

## 🧐 About <a name = "about"></a>

**Bragi - (god of poetry in [Norse mythology](https://en.wikipedia.org/wiki/Bragi)):**
Bragibooks provides a minimal and straightforward webserver that you can run remotely or locally on your server. Since Bragibooks runs in a docker, you no longer need to install dependencies on whichever OS you are on. You can

Some basics of what Bragi does:
- Merge multiple files
- Convert mp3(s)
- Cleanup existing data on an m4b file
- More features on [m4b-merge's help page](https://github.com/djdembeck/m4b-merge)

### Screens

Folder/file selection             |  ASIN input
:-------------------------:|:-------------------------:
![file-selection](../assets/screens/file_picker.png)  |  ![asin-auto-search](../assets/screens/auto_search_panel.png)

Folder/file selection             |  Post-proccess overview
:-------------------------:|:-------------------------:
![asin-custom-search](../assets/screens/custom_search.png)  |  ![post-process](../assets/screens/processing_panel.png)

## 🏁 Getting Started <a name = "getting_started"></a>

You can either install this project directly or run it prepackaged in Docker.

### Prerequisites

#### Docker
- All prerequisites are included in the image.

#### Direct (Gunicorn)
- Install [m4b-merge](https://github.com/djdembeck/m4b-merge) and its dependencies
- Run `pip install -r requirements.txt` from this project's directory.

### Installing

#### Docker
To run Bragibooks as a container, you need to pass some paramaters in the run command:

| Parameter | Function | Default |
| :----: | --- | --- |
| `-v /path/to/input:/input` | Input folder | - |
| `-v /path/to/output:/output` | Output folder | - |
| `-v /appdata/bragibooks/config:/config` | Persistent config storage | - |
| `-p 8000:8000/tcp` | Port for your browser to use | - |
| `-e LOG_LEVEL=INFO` | Choose any [logging level](https://www.loggly.com/ultimate-guide/python-logging-basics/) | INFO |
| `-e DEBUG=False` | Turn django debug on or off (default False) | False |
| `-e UID=99` | User ID to run the container as | 99 |
| `-e GID=100` | Group ID to run the container as | 100 |
| `-e CELERY_WORKERS=1` | The number of celery workers for processing books | 1 |
| `-e CSRF_TRUSTED_ORIGINS=https://bragibooks.mydomain.com` | Domains to trust if bragibooks is hosted behind a reverse proxy (comma-separated) | None |
| `-e ALLOWED_HOSTS=localhost,127.0.0.1` | Comma-separated list of allowed hostnames for Django (comma-separated) | localhost,127.0.0.1 |
| `-e BROKER_URL=sqlite:////config/db.sqlite3` | Celery broker URL for task queue | sqlite:///db.sqlite3 |
| `-e REGION=us` | Audible API region for metadata lookup (e.g., "us", "uk", "de") | us |


Which all together should look like:

	docker run --rm -d --name bragibooks -v /path/to/input:/input -v /path/to/output:/output -v /appdata/bragibooks/config:/config -p 8000:8000/tcp -e LOG_LEVEL=WARNING ghcr.io/djdembeck/bragibooks:main

## Docker Compose
```
version: '3'

services:
  bragi:
    image: ghcr.io/djdembeck/bragibooks:main
    container_name: bragibooks
    environment:
      - CSRF_TRUSTED_ORIGINS=https://bragibooks.mydomain.com
      - LOG_LEVEL=INFO
      - DEBUG=False
      - UID=1000
      - GID=1000
      - ALLOWED_HOSTS=localhost,127.0.0.1,bragibooks.mydomain.com
      - REGION=us
      - CELERY_WORKERS=1
    volumes:
      - path/to/config:/config
      - path/to/input:/input
      # Optional: Mount separate volume for completed files
      # - path/to/done:/input/done
      - path/to/output:/output
    ports:
      - 8000:8000
    restart: unless-stopped
```


#### Direct Build (Gunicorn)
  - Copy static assets to  project folder:
    ```
    python manage.py collectstatic
    ```
  - Create the database:
    ```
    python manage.py migrate
    ```
  - Run the celery worker for processing books:
    ```
    celery -A bragibooks_proj worker \
    --loglevel=info \ 
    --concurrency 1 \
    -E
    ```
  - Run the web server:
    ```
    gunicorn bragibooks_proj.wsgi \
    --bind 0.0.0.0:8000 \
    --timeout 1200 \
    --worker-tmp-dir /dev/shm \
    --workers=2 \
    --threads=4 \
    --worker-class=gthread \
    --reload \
    --enable-stdio-inheritance
    ```

## 🎈 Usage <a name="usage"></a>

The Bragibooks process is a linear, 3 step process:
1. __Select input__ - Use the file multi-select box to choose which books to process this session, and click next.
2. __Submit ASINs__ - Bragi will auto search for the audiobook data on [Audible.com](https://www.audible.com) (US only). If the data found is incorrect you can do a custom search to find the correct title and then submit for processing.
3. Wait for books to finish processing. This can take anywhere from 10 seconds to a few hours, depending on the number and type of files submitted. This will be done in the background.
4. __Books page__ - Page where you can see the data assigned to each book after it has finished processing. You can also check the status of the books still being processed.

## 📋 Environment Variables <a name="env_vars"></a>

All environment variables can be set in Docker with the `-e` flag or in docker-compose.

| Variable | Description | Default |
|----------|-------------|---------|
| `LOG_LEVEL` | Python logging level (DEBUG, INFO, WARNING, ERROR, CRITICAL) | INFO |
| `DEBUG` | Django debug mode (true/false) | false |
| `UID` | User ID for running processes inside container | 99 |
| `GID` | Group ID for running processes inside container | 100 |
| `CELERY_WORKERS` | Number of celery worker processes for background tasks | 1 |
| `CSRF_TRUSTED_ORIGINS` | Comma-separated list of trusted origins for CSRF protection | None |
| `ALLOWED_HOSTS` | Comma-separated list of allowed hostnames for Django | localhost,127.0.0.1 |
| `BROKER_URL` | Celery broker URL (Database or Redis) | sqlite:///db.sqlite3 |
| `REGION` | Audible API region code (us, uk, de, fr, etc.) | us |

### Production Security Headers

When `DEBUG=False`, Bragibooks automatically enables production security headers:
- `SECURE_SSL_REDIRECT` - Redirects HTTP to HTTPS
- `SECURE_HSTS_SECONDS=31536000` - HSTS with 1 year max-age
- `SECURE_HSTS_INCLUDE_SUBDOMAINS` - Include all subdomains in HSTS
- `SECURE_HSTS_PRELOAD` - Allow HSTS preloading
- `SECURE_CONTENT_TYPE_NOSNIFF` - Prevent MIME type sniffing
- `X_FRAME_OPTIONS=DENY` - Prevent clickjacking
- `CSRF_COOKIE_SECURE=True` - Secure CSRF cookies
- `SESSION_COOKIE_SECURE=True` - Secure session cookies

## ⛏️ Built Using <a name = "built_using"></a>

- [Django 5.2 LTS](https://www.djangoproject.com/) - Server/web framework
- [Celery](https://docs.celeryq.dev/en/stable/getting-started/introduction.html) - Task queue and worker
- [Bulma](https://bulma.io/) - Frontend CSS framework
- [audnexus](https://github.com/laxamentumtech/audnexus) - API backend for metadata
- [m4b-merge (Rust)](https://github.com/djdembeck/m4b-merge) - High-performance file merging and tagging
- [Python 3.10+](https://www.python.org/) - Application runtime
- [SQLite](https://www.sqlite.org/) - Database (default)

## ✍️ Authors <a name = "authors"></a>
  <img src="https://github.com/djdembeck.png?size=100"/>
  
  [@djdembeck](https://github.com/djdembeck) - Idea & Initial work

## Contributors ✨

Thanks goes to these wonderful people ([emoji key](https://allcontributors.org/docs/en/emoji-key)):

<!-- ALL-CONTRIBUTORS-LIST:START - Do not remove or modify this section -->
<!-- prettier-ignore-start -->
<!-- markdownlint-disable -->
<table>
  <tbody>
    <tr>
      <td align="center" valign="top" width="14.28%"><a href="https://koby.huckabee.dev"><img src="https://avatars.githubusercontent.com/u/14910857?v=4?s=100" width="100px;" alt="Koby Huckabee"/><br /><sub><b>Koby Huckabee</b></sub></a><br /><a href="https://github.com/djdembeck/bragibooks/commits?author=AceTugboat" title="Code">💻</a> <a href="#ideas-AceTugboat" title="Ideas, Planning, & Feedback">🤔</a> <a href="https://github.com/djdembeck/bragibooks/commits?author=AceTugboat" title="Documentation">📖</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://pilabor.com"><img src="https://avatars.githubusercontent.com/u/2050604?v=4?s=100" width="100px;" alt="Andreas"/><br /><sub><b>Andreas</b></sub></a><br /><a href="#tool-sandreas" title="Tools">🔧</a></td>
    </tr>
  </tbody>
</table>

<!-- markdownlint-restore -->
<!-- prettier-ignore-end -->

<!-- ALL-CONTRIBUTORS-LIST:END -->

This project follows the [all-contributors](https://github.com/all-contributors/all-contributors) specification. Contributions of any kind welcome!