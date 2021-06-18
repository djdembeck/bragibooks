# Bragi Books
**Bragi - (god of poetry in [Norse mythology](https://en.wikipedia.org/wiki/Bragi)):**
An audiobook library cleanup & management app, written for web use with Django. Leveraging Audible's partial API to source metadata.
Successor of [m4b-merge](https://github.com/djdembeck/m4b-merge)

## Requirements
- [m4b-tool](https://github.com/sandreas/m4b-tool) by [sandreas](https://github.com/sandreas)
    - [m4b-tool's list of dependencies](https://github.com/sandreas/m4b-tool#ubuntu)
- [requirements.txt](requirements.txt)

- Django:
  - Set `rootdir` to a full-path input dir, in [views.py](importer/views.py)
  - Create `secret_key.txt` and [fill it with a large password](https://docs.djangoproject.com/en/3.2/ref/settings/#std:setting-SECRET_KEY).
  - `python manage.py makemigrations`
  - `python manage.py migrate`
  - `python manage.py runserver`

- Docker:
  - To run the container, you need to pass the input folder and port you would like to use:
    - Edit the `/home/user` portion: `-v /home/user/input:/root/input`
    - `-p 8000:8000/tcp`
  - Which all together should look like: ```docker run --rm -d -v /home/user/input:/root/input -p 8000:8000/tcp bragibooks:latest```

- CLI:
  - You can and _should_ run the core logic from terminal. I'm planning a docker to make this even easier to automate.
  - Check the user editable variables in [merge_cli.py](importer/merge_cli.py), and see if there's anything you need to change.
  - After that, just run `python importer/manage_cli.py` and it will walk you through what you need to get going.

## Notes
- About API auth method: This project uses the [Audible for Python](https://github.com/mkb79/Audible) module by [mkb79](https://github.com/mkb79) for it's auth/api calls. For persistent authentication, Bragi uses the [Register an Audible device](https://audible.readthedocs.io/en/latest/auth/register.html) method of logging in, to avoid asking for log in constantly.
- Not everything is finished porting from the old `m4b-merge` project, but what has been ported to Bragi is significantly more resilient.
