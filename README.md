[![GitHub](https://img.shields.io/github/license/djdembeck/bragibooks)](https://github.com/djdembeck/bragibooks/blob/develop/LICENSE)
[![Docker](https://github.com/djdembeck/bragibooks/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/djdembeck/bragibooks/actions/workflows/docker-publish.yml)
[![Docker Pulls](https://img.shields.io/docker/pulls/djdembeck/bragibooks)](https://hub.docker.com/r/djdembeck/bragibooks)
[![Docker Image Size (latest by date)](https://img.shields.io/docker/image-size/djdembeck/bragibooks)](https://hub.docker.com/r/djdembeck/bragibooks)
[![Docker Image Version (latest by date)](https://img.shields.io/docker/v/djdembeck/bragibooks)](https://hub.docker.com/r/djdembeck/bragibooks)
[![CodeFactor Grade](https://img.shields.io/codefactor/grade/github/djdembeck/bragibooks)](https://www.codefactor.io/repository/github/djdembeck/bragibooks)

![Logo](../assets/logo-horizontal.png?raw=true)
**Bragi - (god of poetry in [Norse mythology](https://en.wikipedia.org/wiki/Bragi)):**
An audiobook library cleanup & management app, written for both web use (Django) and CLI (Python). Leveraging Audible's unofficial API to source metadata. Core logic and processing done by my other tool, [m4b-merge](https://github.com/djdembeck/m4b-merge)

## Screens

Folder/file selection             |  ASIN input
:-------------------------:|:-------------------------:
![screenshot-10 0 0 254_8000-2021 06 19-17_04_43](https://user-images.githubusercontent.com/71412966/122656488-ab6ae480-d120-11eb-9893-692fd1428240.png)  |  ![screenshot-10 0 0 254_8000-2021 06 19-17_05_13](https://user-images.githubusercontent.com/71412966/122656487-ab6ae480-d120-11eb-97ed-3e21c598616d.png)

### Book page after file conversion
![screenshot-10 0 0 254_8000-2021 06 19-17_09_10](https://user-images.githubusercontent.com/71412966/122656539-1ddbc480-d121-11eb-9066-a7ca6d13c560.png)

## Requirements
- [m4b-tool](https://github.com/sandreas/m4b-tool) by [sandreas](https://github.com/sandreas)
    - [m4b-tool's list of dependencies](https://github.com/sandreas/m4b-tool#ubuntu)
- `pip install -r requirements.txt`

## Running

### Docker (recommended):
To run Bragi Books as a container, you need to pass some paramaters in the run command:

| Parameter | Function |
| :----: | --- |
| `-v /path/to/input:/input` | Input folder |
| `-v /path/to/output:/output` | Output folder |
| `-v /appdata/bragibooks/config:/config` | Persistent config storage |
| `-p 8000:8000/tcp` | Port for your browser to use |
| `-e LOG_LEVEL=WARNING` | Choose any [logging level](https://www.loggly.com/ultimate-guide/python-logging-basics/) |

    
Which all together should look like: 

	docker run --rm -d --name bragibooks -v /path/to/input:/input -v /path/to/output:/output -v /appdata/bragibooks/config:/config -p 8000:8000/tcp -e LOG_LEVEL=WARNING ghcr.io/djdembeck/bragibooks:main
	
---

### Webserver (Gunicorn + Django):
From within the `bragibooks` folder you cloned:
  - Install [m4b-merge](https://github.com/djdembeck/m4b-merge) and it's requirements
  - `python manage.py migrate`
  - `gunicorn bragibooks_proj.wsgi`

---

## Notes
- About API auth method: This project uses the [Audible for Python](https://github.com/mkb79/Audible) module by [mkb79](https://github.com/mkb79) for it's auth/api calls. For persistent authentication, Bragi uses the [Register an Audible device](https://audible.readthedocs.io/en/latest/auth/register.html) method of logging in, to avoid asking for log in constantly.
