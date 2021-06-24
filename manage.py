#!/usr/bin/env python
"""Django's command-line utility for administrative tasks."""
import os
import subprocess, sys

# Build paths inside the project like this: os.path.join(BASE_DIR, ...)
BASE_DIR = os.path.dirname(os.path.abspath(__file__))

# config section for docker
if os.path.isdir("/config"):
	CONFIG_DIR = os.path.abspath("/config")
else:
	CONFIG_DIR = os.path.join(BASE_DIR, 'config')
	os.makedirs(CONFIG_DIR, exist_ok=True)

SECRET_PATH = os.path.join(CONFIG_DIR, 'secret_key.txt')

# Init django secret and DB
from django.core.management.utils import get_random_secret_key
if not os.path.exists(SECRET_PATH):
	f = open(SECRET_PATH, "w")
	f.write(get_random_secret_key())
	f.close()
	subprocess.run(["python", "manage.py", "makemigrations"])
	subprocess.run(["python", "manage.py", "migrate"])

def main():
    os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'bragibooks_proj.settings')
    try:
        from django.core.management import execute_from_command_line
    except ImportError as exc:
        raise ImportError(
            "Couldn't import Django. Are you sure it's installed and "
            "available on your PYTHONPATH environment variable? Did you "
            "forget to activate a virtual environment?"
        ) from exc
    execute_from_command_line(sys.argv)


if __name__ == '__main__':
    main()
