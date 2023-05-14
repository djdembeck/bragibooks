import logging
import os
import traceback
from datetime import datetime
from pathlib import Path

from django.conf import settings
# core merge logic:
from m4b_merge import audible_helper, config, helpers, m4b_helper

from importer.models import (Author, Book, Narrator, Setting, Status,
                             StatusChoices)

# Get an instance of a logger
logger = logging.getLogger(__name__)


def set_configs():
    existing_settings = Setting.objects.first()
    if existing_settings:
        config.api_url = existing_settings.api_url
        config.junk_dir = existing_settings.completed_directory
        config.num_cpus = (
            existing_settings.num_cpus if existing_settings.num_cpus > 0
            else os.cpu_count()
        )
        config.output = existing_settings.output_directory
        config.path_format = existing_settings.output_scheme


def run_m4b_merge(asin: str):
    # Log Level
    env_log_level = os.environ.get("LOG_LEVEL", "INFO")
    logging.basicConfig(level=env_log_level)

    set_configs()

    # Log all Settings
    logger.debug(f'Using API URL: {config.api_url}')
    logger.debug(f'Using junk path: {config.junk_dir}')
    logger.debug(f'Using CPU cores: {config.num_cpus}')
    logger.debug(f'Using output path: {config.output}')
    logger.debug(f'Using output format: {config.path_format}')

    book = Book.objects.get(asin=asin)
    logger.info(
        f"{'-' * 15} Starting to process {asin}: {book.title} {'-' * 15}")

    input_data = helpers.get_directory(Path(book.src_path))
    if not input_data:
        message = f"invalid input_data: {input_data} for book: {book} at path: {book.src_path}"
        logger.error(message)
        book.status.status = StatusChoices.ERROR
        book.status.message = message
        book.status.save()
        return

    audible = audible_helper.BookData(asin)

    # Process metadata and run components to merge files
    m4b = m4b_helper.M4bMerge(
        input_data,
        audible.fetch_api_data(config.api_url),
        Path(book.src_path),
        audible.get_chapters()
    )

    try:
        logger.info(f"Processing {book.title}")
        m4b.run_merge()
    except Exception as e:
        logger.error(f"Error occured while merging '{input_data}: {e}'")
        book.status.status = StatusChoices.ERROR
        message = str(e) + "\n" + \
            traceback.format_exc() if settings.DEBUG else e
        book.status.message = message
        book.status.save()
        return

    book.dest_path = Path(
        f"\""
        f"{m4b.book_output}/"
        f"{audible.fetch_api_data(config.api_url)['authors'][0]}/"
        f"{book.title}/"
        f"{book.title}.m4b"
        f"\""
    )
    book.status.status = StatusChoices.DONE
    book.status.save()
    logger.info(f"{'-' * 15} Done processing {asin} {'-' * 15}")


def create_book(asin, original_path) -> Book:
    # Make models only if book doesn't exist
    if not Book.objects.filter(asin=asin).exists():
        book = make_book_model(asin, original_path)

    else:
        book = Book.objects.get(asin=asin)
        book.src_path = original_path
        book.status.status = StatusChoices.PROCESSING
        book.status.message = ""
        book.status.save()
        logger.warning("Book already exists in database, only merging files")

    return book


def make_book_model(asin, original_path) -> Book:
    set_configs()
    # Create BookData object from asin response
    metadata = audible_helper.BookData(asin).fetch_api_data(config.api_url)

    # Book DB entry
    if 'subtitle' in metadata:
        base_title = metadata['title']
        base_subtitle = metadata['subtitle']
        title = f"{base_title} - {base_subtitle}"
    else:
        title = metadata['title']

    if 'runtimeLengthMin' in metadata:
        runtime = metadata['runtimeLengthMin']
    else:
        runtime = 0

    status = Status.objects.create(status=StatusChoices.PROCESSING)

    book = Book.objects.create(
        title=title,
        asin=asin,
        short_desc=metadata['description'],
        long_desc=metadata['summary'],
        release_date=datetime.strptime(
            metadata['releaseDate'], '%Y-%m-%dT%H:%M:%S.%fZ'),
        publisher=metadata['publisherName'],
        lang=metadata['language'],
        runtime_length_minutes=runtime,
        format_type=metadata['formatType'],
        converted=True,
        status=status,
        src_path=original_path
    )

    # Only add in series if it exists
    if 'primarySeries' in metadata:
        book.series = metadata['primarySeries']['name']
        book.save()

    make_author_model(book, metadata['authors'])
    make_narrator_model(book, metadata['narrators'])

    return book


def make_author_model(book, authors: list[dict[str, str]]):
    # Author DB entry
    # Create new entry for each author if there's more than one
    for author in authors:
        author_name_full = author['name']
        author_name_split = author_name_full.split()
        last_name_index = len(author_name_split) - 1

        # Check if author asin exists
        if 'asin' in author:
            author_asin = author['asin']
            _filter_vals = {'asin': author_asin}

        # If author doesn't exist, search by name and set asin to none
        else:
            author_asin = None
            _filter_vals = {
                'first_name': author_name_split[0],
                'last_name': author_name_split[last_name_index]
            }
            logger.warning(
                f"No author ASIN for: "
                f"{author_name_full}"
            )

        # Check if author is in database
        if not (author := Author.objects.filter(**_filter_vals).first()):
            logger.info(
                f"Using existing db entry for author: "
                f"{author_name_full}"
            )
            author = Author.objects.create(
                asin=author_asin,
                first_name=author_name_split[0],
                last_name=author_name_split[last_name_index]
            )

        author.books.add(book)
        author.save()


def make_narrator_model(book, narrators: list[dict[str, str]]):
    # Narrator DB entry
    # Create new entry for each narrator if there's more than one
    for narrator in narrators:

        narr_name_split = narrator['name'].split()
        last_name_index = len(narr_name_split) - 1

        if not (narrator := Narrator.objects.filter(
            first_name=narr_name_split[0],
            last_name=narr_name_split[last_name_index]
        ).first()):
            narrator = Narrator.objects.create(
                first_name=narr_name_split[0],
                last_name=narr_name_split[last_name_index]
            )

        narrator.books.add(book)
        narrator.save()
