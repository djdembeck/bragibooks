from bragibooks_proj.celery import app as celery_app
from utils.merge import run_m4b_merge


@celery_app.task
def m4b_merge_task(asin: str):
    run_m4b_merge(asin=asin)
