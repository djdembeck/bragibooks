# Bragi Books
**Bragi - (god of poetry in [Norse mythology](https://en.wikipedia.org/wiki/Bragi)):**
An audiobook library cleanup & management app, written for both web use (Django) and CLI (Python). Leveraging Audible's unofficial API to source metadata. Successor of [m4b-merge](https://github.com/djdembeck/m4b-merge)

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

	docker run --rm -d -v /path/to//input:/input -v /path/to//output:/output -v /appdata/bragibooks/config:/config -p 8000:8000/tcp -e LOG_LEVEL=WARNING bragibooks:latest
	
---

### Webserver (Django):
  - `python manage.py makemigrations`
  - `python manage.py migrate`
  - `python manage.py runserver`

---

### CLI:
You can run the core logic from the terminal without any running server or database

  - Check the user editable variables in [merge_cli.py](importer/merge_cli.py), and see if there's anything you need to change.
  - After that, just run `python importer/manage_cli.py` and it will walk you through what you need to get going.

---

## Notes
- About API auth method: This project uses the [Audible for Python](https://github.com/mkb79/Audible) module by [mkb79](https://github.com/mkb79) for it's auth/api calls. For persistent authentication, Bragi uses the [Register an Audible device](https://audible.readthedocs.io/en/latest/auth/register.html) method of logging in, to avoid asking for log in constantly.
