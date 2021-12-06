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


</div>

---

<p align="center"> An audiobook library cleanup & management app, written as a frontend for web use of <a href="https://github.com/djdembeck/m4b-merge">m4b-merge</a>.
    <br> 
</p>

## 📝 Table of Contents

- [About](#about)
- [Getting Started](#getting_started)
- [Usage](#usage)
- [Built Using](#built_using)
- [Contributing](../CONTRIBUTING.md)
- [Authors](#authors)
- [Acknowledgments](#acknowledgement)

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
![file-selection](../assets/screens/file-selection.png)  |  ![asin-pre](../assets/screens/asin-pre.png)

Folder/file selection             |  Post-proccess overview
:-------------------------:|:-------------------------:
![asin-input](../assets/screens/asin-input.png)  |  ![post-process](../assets/screens/post-process.png)

## 🏁 Getting Started <a name = "getting_started"></a>

You can either install this project directly or run it prepackaged in Docker.

### Prerequisites

#### Docker
- All prerequisites are included in the image.

#### Direct (Gunicorn)
- You'll need to install m4b-tool and it's dependants from [the project's readme](https://github.com/sandreas/m4b-tool#installation)
- Run `pip install -r requirements.txt` from this project's directory.

### Installing

#### Docker
To run Bragibooks as a container, you need to pass some paramaters in the run command:

  | Parameter | Function |
  | :----: | --- |
  | `-v /path/to/input:/input` | Input folder |
  | `-v /path/to/output:/output` | Output folder |
  | `-v /appdata/bragibooks/config:/config` | Persistent config storage |
  | `-p 8000:8000/tcp` | Port for your browser to use |
  | `-e LOG_LEVEL=WARNING` | Choose any [logging level](https://www.loggly.com/ultimate-guide/python-logging-basics/) |


Which all together should look like: 

	docker run --rm -d --name bragibooks -v /path/to/input:/input -v /path/to/output:/output -v /appdata/bragibooks/config:/config -p 8000:8000/tcp -e LOG_LEVEL=WARNING ghcr.io/djdembeck/bragibooks:main

#### Direct (Gunicorn)
  - Copy static assets to  project folder:
    ```
    python manage.py collectstatic
    ```
  - Create the database:
    ```
    python manage.py migrate
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
2. __Submit ASINs__ - Enter the 10-character ASIN for each book, found on [Audible.com](https://www.audible.com) (US only) and submit.
3. Wait for books to finish processing. This can take anywhere from 10 seconds to a few hours, depending on the number and type of files submitted.
4. __Finish page__ - Page where you can see the data assigned to each book after it has finished processing.

## ⛏️ Built Using <a name = "built_using"></a>

- [Django](https://www.djangoproject.com/) - Server/web framework
- [Bulma](https://bulma.io/) - Frontend CSS framework
- [audnexus](https://github.com/laxamentumtech/audnexus) - API backend for metadata
- [m4b-merge](https://github.com/djdembeck/m4b-merge) - File merging and tagging

## ✍️ Authors <a name = "authors"></a>

- [@djdembeck](https://github.com/djdembeck) - Idea & Initial work

## 🎉 Acknowledgements <a name = "acknowledgement"></a>

- [sandreas](https://github.com/sandreas) for creating and maintaining [m4b-tool](https://github.com/sandreas/m4b-tool)