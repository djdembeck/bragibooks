import logging
import os
import re
import subprocess
import traceback
from datetime import datetime
from pathlib import Path

import requests
from django.conf import settings

from importer.models import Author, Book, Narrator, Setting, Status, StatusChoices

# Get an instance of a logger
logger = logging.getLogger(__name__)


def set_configs():
    """Get settings from database and return CLI arguments dict for subprocess."""
    existing_settings = Setting.objects.first()
    if not existing_settings:
        return None

    return {
        "api_url": existing_settings.api_url,
        "completed_directory": existing_settings.completed_directory,
        "num_cpus": (
            existing_settings.num_cpus
            if existing_settings.num_cpus > 0
            else (os.cpu_count() or 1)
        ),
        "output_directory": existing_settings.output_directory,
        "path_format": existing_settings.output_scheme,
    }


def fetch_audible_metadata(asin: str, api_url: str = "https://api.audnex.us") -> dict:
    """Fetch book metadata from Audible API using ASIN."""
    try:
        response = requests.get(f"{api_url}/books/{asin}", timeout=30)
        response.raise_for_status()
        return response.json()
    except requests.RequestException as e:
        logger.error(f"Failed to fetch metadata for ASIN {asin}: {e}")
        raise


def run_m4b_merge(asin: str):
    # Log Level
    env_log_level = os.environ.get("LOG_LEVEL", "INFO")
    logging.basicConfig(level=env_log_level)

    # Get settings for CLI arguments
    cli_args = set_configs()
    if not cli_args:
        message = "No settings found in database"
        logger.error(message)
        book = Book.objects.get(asin=asin)
        book.status.status = StatusChoices.ERROR
        book.status.message = message
        book.status.save()
        return

    # Log all Settings
    logger.debug(f"Using API URL: {cli_args['api_url']}")
    logger.debug(f"Using completed directory: {cli_args['completed_directory']}")
    logger.debug(f"Using CPU cores: {cli_args['num_cpus']}")
    logger.debug(f"Using output path: {cli_args['output_directory']}")
    logger.debug(f"Using output format: {cli_args['path_format']}")

    book = Book.objects.get(asin=asin)
    logger.info(f"{'-' * 15} Starting to process {asin}: {book.title} {'-' * 15}")

    # Resolve absolute path for input directory
    src_path = Path(book.src_path).resolve()
    if not src_path.exists():
        message = f"Input path does not exist: {src_path}"
        logger.error(message)
        book.status.status = StatusChoices.ERROR
        book.status.message = message
        book.status.save()
        return

    # Build CLI arguments list for subprocess
    cmd = [
        "m4b-merge",
        "--inputs",
        str(src_path),
        "--asin",
        asin,
        "--api-url",
        cli_args["api_url"],
        "--output",
        cli_args["output_directory"],
        "--completed-directory",
        cli_args["completed_directory"],
        "--num-cpus",
        str(cli_args["num_cpus"]),
        "--path-format",
        cli_args["path_format"],
        "--log-level",
        env_log_level,
    ]

    logger.info(f"Running: {' '.join(cmd)}")

    try:
        result = subprocess.run(
            cmd,
            timeout=14400,  # 4 hours default timeout
            capture_output=True,
            text=True,
        )
    except subprocess.TimeoutExpired:
        message = f"m4b-merge timed out after 4 hours for ASIN: {asin}"
        logger.error(message)
        book.status.status = StatusChoices.ERROR
        book.status.message = message
        book.status.save()
        # Re-raise to allow Celery retry mechanism to work
        raise
    except OSError as e:
        message = f"m4b-merge failed with OSError for ASIN: {asin}: {e}"
        logger.error(message)
        book.status.status = StatusChoices.ERROR
        book.status.message = message
        book.status.save()
        # Re-raise to allow Celery retry mechanism to work
        raise

    # Log output for debugging
    if result.stdout:
        logger.debug(f"m4b-merge stdout: {result.stdout}")
    if result.stderr:
        logger.debug(f"m4b-merge stderr: {result.stderr}")

    # Check exit code
    if result.returncode != 0:
        message = (
            f"m4b-merge failed with exit code {result.returncode}: {result.stderr}"
        )
        logger.error(message)
        book.status.status = StatusChoices.ERROR
        book.status.message = message
        book.status.save()
        return

    # Parse output to extract output file path
    # Rust binary outputs: "1. /path/to/output/file.m4b"
    dest_path = None
    output_pattern = r"^\d+\.\s+(.+)$"
    for line in result.stdout.splitlines():
        match = re.match(output_pattern, line.strip())
        if match:
            dest_path = Path(match.group(1))
            break

    if dest_path:
        book.dest_path = str(dest_path)
        book.save(update_fields=["dest_path"])
        logger.info(f"Output file: {dest_path}")
    else:
        # Parse failure - treat as error
        message = f"Could not parse output path from Rust output: {result.stdout}"
        logger.error(message)
        book.status.status = StatusChoices.ERROR
        book.status.message = message
        book.status.save()
        raise ValueError(message)

    book.status.status = StatusChoices.DONE
    book.status.message = ""
    book.status.save()
    logger.info(f"{'-' * 15} Done processing {asin} {'-' * 15}")


def create_book(asin, original_path) -> Book:
    # Make models only if book doesn't exist
    if not Book.objects.filter(asin=asin).exists():
        book = make_book_model(asin, original_path)

    else:
        book = Book.objects.get(asin=asin)
        book.src_path = original_path
        book.save()

        book.status.status = StatusChoices.PROCESSING
        book.status.message = ""
        book.status.save()
        logger.warning("Book already exists in database, only merging files")

    return book


def make_book_model(asin, original_path) -> Book:
    # Get API URL from settings
    cli_args = set_configs()
    api_url = cli_args["api_url"] if cli_args else "https://api.audnex.us"

    # Fetch metadata from Audible API
    metadata = fetch_audible_metadata(asin, api_url)

    # Book DB entry
    if "subtitle" in metadata:
        base_title = metadata["title"]
        base_subtitle = metadata["subtitle"]
        title = f"{base_title} - {base_subtitle}"
    else:
        title = metadata["title"]

    if "runtimeLengthMin" in metadata:
        runtime = metadata["runtimeLengthMin"]
    else:
        runtime = 0

    status = Status.objects.create(status=StatusChoices.PROCESSING)

    book = Book.objects.create(
        title=title,
        asin=asin,
        short_desc=metadata["description"],
        long_desc=metadata["summary"],
        release_date=datetime.strptime(
            metadata["releaseDate"], "%Y-%m-%dT%H:%M:%S.%fZ"
        ),
        publisher=metadata["publisherName"],
        lang=metadata["language"],
        runtime_length_minutes=runtime,
        format_type=metadata["formatType"],
        converted=True,
        status=status,
        cover_image_link=metadata["image"],
        src_path=original_path,
    )

    # Only add in series if it exists
    if "primarySeries" in metadata:
        book.series = metadata["primarySeries"]["name"]
        book.save()

    make_author_model(book, metadata["authors"])
    make_narrator_model(book, metadata["narrators"])

    return book


def make_author_model(book, authors: list[dict[str, str]]):
    # Author DB entry
    # Create new entry for each author if there's more than one
    for author in authors:
        author_name_full = author["name"]
        author_name_split = author_name_full.split()
        last_name_index = len(author_name_split) - 1

        # Check if author asin exists
        if "asin" in author:
            author_asin = author["asin"]
            _filter_vals = {"asin": author_asin}

        # If author doesn't exist, search by name and set asin to none
        else:
            author_asin = None
            _filter_vals = {
                "first_name": author_name_split[0],
                "last_name": author_name_split[last_name_index],
            }
            logger.warning(f"No author ASIN for: {author_name_full}")

        # Check if author is in database
        if not (author := Author.objects.filter(**_filter_vals).first()):
            logger.info(f"Creating new db entry for author: {author_name_full}")
            author = Author.objects.create(
                asin=author_asin,
                first_name=author_name_split[0],
                last_name=author_name_split[last_name_index],
            )

        author.books.add(book)
        author.save()


def make_narrator_model(book, narrators: list[dict[str, str]]):
    # Narrator DB entry
    # Create new entry for each narrator if there's more than one
    for narrator in narrators:
        narr_name_split = narrator["name"].split()
        last_name_index = len(narr_name_split) - 1

        if not (
            narrator := Narrator.objects.filter(
                first_name=narr_name_split[0],
                last_name=narr_name_split[last_name_index],
            ).first()
        ):
            narrator = Narrator.objects.create(
                first_name=narr_name_split[0],
                last_name=narr_name_split[last_name_index],
            )

        narrator.books.add(book)
        narrator.save()
