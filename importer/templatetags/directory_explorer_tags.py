import logging
from pathlib import Path
import uuid
from django import template
import os

logger = logging.getLogger(__name__)
register = template.Library()


@register.filter
def is_directory(path):
    return os.path.isdir(path)


@register.filter
def basename(path):
    return os.path.basename(path)


@register.filter
def add_one(num):
    if num:
        return num + 1
    else:
        return 1


@register.filter
def get_range(num):
    if num:
        return [i for i in range(int(num))]


@register.filter
def generate_id(_):
    return uuid.uuid4()


def directory_contents(path):
    """
    Returns a list of files and subdirectories in the given path.
    """
    return sorted(Path(path).iterdir(), key=os.path.getmtime, reverse=True)


@register.inclusion_tag("directory_contents.html")
def render_directory(path, folder_id, depth):
    """
    Renders a directory and its contents as a nested list.
    """
    contents = directory_contents(path)
    return {
        "path": path,
        "contents": contents,
        "display": "none" if int(depth) > 0 else "",
        "folder_id": f"{folder_id}",
        "depth": depth,
    }
