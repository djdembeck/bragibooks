import logging
from importer.models import Book, Author, Narrator
# core merge logic:
from m4b_merge import audible_helper, m4b_helper

# Get an instance of a logger
logger = logging.getLogger(__name__)


# Class to handle merge and model generation operations
class Merge:
    def __init__(self, asin, input_data, original_path):
        self.asin = asin
        self.input_data = input_data
        self.original_path = original_path

    def run_m4b_merge(self):
        # Create BookData object from asin response
        aud = audible_helper.BookData(self.asin)
        self.metadata = aud.parser()
        self.chapters = aud.get_chapters()

        # Process metadata and run components to merge files
        self.m4b = m4b_helper.M4bMerge(
            self.input_data, self.metadata, self.chapters
        )
        self.m4b.run_merge()

        # Make models only if book doesn't exist
        if not Book.objects.filter(asin=self.asin):
            self.new_book = self.make_book_model()
            self.make_author_model()
            self.make_narrator_model()
        else:
            logger.warning(
                "Book already exists in database, only merging files"
            )

    def make_book_model(self):
        # Book DB entry
        if 'subtitle' in self.metadata:
            base_title = self.metadata['title']
            base_subtitle = self.metadata['subtitle']
            title = f"{base_title} - {base_subtitle}"
        else:
            title = self.metadata['title']

        new_book = Book.objects.create(
            title=self.metadata['title'],
            asin=self.asin,
            short_desc=self.metadata['short_summary'],
            long_desc=self.metadata['long_summary'],
            release_date=self.metadata['release_date'],
            publisher=self.metadata['publisher_name'],
            lang=self.metadata['language'],
            runtime_length_minutes=self.metadata['runtime_length_min'],
            format_type=self.metadata['format_type'],
            converted=True,
            src_path=self.original_path,
            dest_path=(
                f"\""
                f"{self.m4b.book_output}/"
                f"{self.metadata['authors'][0]}/"
                f"{self.metadata['title']}/"
                f"{title}.m4b"
                f"\""
            )
        )

        # Only add in series if it exists
        if 'series' in self.metadata:
            new_book.series = self.metadata['series']

        new_book.save()
        return new_book

    def make_author_model(self):
        # Author DB entry
        # Create new entry for each author if there's more than one
        if len(self.metadata['authors']) > 1:
            for author in self.metadata['authors']:
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
                if not Author.objects.filter(
                    **_filter_vals
                ):
                    new_author = Author.objects.create(
                        asin=author_asin,
                        first_name=author_name_split[0],
                        last_name=author_name_split[last_name_index]
                    )
                    new_author.books.add(self.new_book)
                    new_author.save()
                else:
                    logger.info(
                        f"Using existing db entry for author: "
                        f"{author_name_full}"
                    )
                    author_id = Author.objects.filter(
                        **_filter_vals
                    )
                    existing_author = Author.objects.get(
                        id=author_id[0].id
                    )
                    existing_author.books.add(self.new_book)
                    existing_author.save()
        else:
            author_name_full = self.metadata['authors'][0]['name']
            author_name_split = author_name_full.split()
            last_name_index = len(author_name_split) - 1

            # Check if author asin exists
            if 'asin' in self.metadata['authors'][0]:
                author_asin = self.metadata['authors'][0]['asin']
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
            if not Author.objects.filter(
                **_filter_vals
            ):
                new_author = Author.objects.create(
                    asin=author_asin,
                    first_name=author_name_split[0],
                    last_name=author_name_split[last_name_index]
                )
                new_author.books.add(self.new_book)
                new_author.save()
            else:
                logger.info(
                    f"Using existing db entry for author: "
                    f"{author_name_full}"
                )
                author_id = Author.objects.filter(
                    **_filter_vals
                )
                existing_author = Author.objects.get(
                    id=author_id[0].id
                )
                existing_author.books.add(self.new_book)
                existing_author.save()

    def make_narrator_model(self):
        # Narrator DB entry
        # Create new entry for each narrator if there's more than one
        if len(self.metadata['narrators']) > 1:
            for narrator in self.metadata['narrators']:
                narr_name_split = narrator.split()
                last_name_index = len(narr_name_split) - 1
                if not Narrator.objects.filter(
                    first_name=narr_name_split[0],
                    last_name=narr_name_split[last_name_index]
                ):
                    new_narrator = Narrator.objects.create(
                        first_name=narr_name_split[0],
                        last_name=narr_name_split[last_name_index]
                    )
                    new_narrator.books.add(self.new_book)
                    new_narrator.save()
                else:
                    existing_narrator = Narrator.objects.get(
                        first_name=narr_name_split[0],
                        last_name=narr_name_split[last_name_index]
                    )
                    existing_narrator.books.add(self.new_book)
                    existing_narrator.save()
        else:
            narr_name_split = self.metadata['narrators'][0].split()
            last_name_index = len(narr_name_split) - 1
            if not Narrator.objects.filter(
                first_name=narr_name_split[0],
                last_name=narr_name_split[last_name_index]
            ):
                new_narrator = Narrator.objects.create(
                    first_name=narr_name_split[0],
                    last_name=narr_name_split[last_name_index]
                )
                new_narrator.books.add(self.new_book)
                new_narrator.save()
            else:
                existing_narrator = Narrator.objects.get(
                    first_name=narr_name_split[0],
                    last_name=narr_name_split[last_name_index]
                )
                existing_narrator.books.add(self.new_book)
                existing_narrator.save()
