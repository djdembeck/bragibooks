import subprocess

from bragibooks_proj.celery import app as celery_app
from importer.models import Book, StatusChoices
from utils.merge import run_m4b_merge


@celery_app.task(
    bind=True,
    autoretry_for=(subprocess.TimeoutExpired, OSError),
    max_retries=3,
    retry_backoff=True,
)
def m4b_merge_task(self, asin: str):
    """Process audiobook merge task with retry logic for subprocess errors."""
    try:
        run_m4b_merge(asin=asin)
        # Check if the merge actually succeeded
        book = Book.objects.get(asin=asin)
        if book.status.status == StatusChoices.ERROR:
            return {"status": "error", "asin": asin, "message": book.status.message}
        return {"status": "success", "asin": asin}
    except (subprocess.TimeoutExpired, OSError):
        # Let Celery's autoretry_for handle these specific exceptions
        raise
    except Exception:
        # Re-raise other exceptions to fail immediately
        raise
