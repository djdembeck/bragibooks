#!/usr/bin/env python
"""Django's command-line utility for administrative tasks."""
import os
import subprocess, sys

# config section for docker
if os.path.isdir("/config"):
	CONFIG_DIR = os.path.abspath("/config")
else:
	CONFIG_DIR = './'

# Init django secret and DB
from django.core.management.utils import get_random_secret_key
if not os.path.exists(f"{CONFIG_DIR}/secret_key.txt"):
	f = open(f"{CONFIG_DIR}/secret_key.txt", "w")
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
