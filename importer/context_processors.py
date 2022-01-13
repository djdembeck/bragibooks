from django import get_version
from importlib.metadata import version
from ._version import __version__

def add_version_to_context(request):
    return {
        'bragibooks_version': __version__,
        'django_version': get_version(),
        'm4b_merge_version': version('m4b_merge')
    }
