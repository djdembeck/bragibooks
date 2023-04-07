# Import internal tools
import logging
import os
import re
import unicodedata
import urllib.parse
from functools import reduce

from django.conf import settings
from Levenshtein import distance

from .region_tools import RegionTool

# Setup logger
logger = logging.getLogger(__name__)

class SearchTool:
    def __init__(self, filename: str, title: str = "", author: str = "", keywords: str = "", region_override: str = ""):
        self.filename = filename
        self.title = title
        self.author = author
        self.keywords = keywords
        self.normalizedFileName = ""
        self.region = region_override or os.environ.get("REGION", "us")

    def build_search_args(self) -> str:
        """
            Builds the search arguments for the API call.
        """
        # First, normalize the name
        if not self.title and not self.author and not self.keywords:
            self.normalizedFileName = self.normalize_name(self.filename)

        query = []

        if self.normalizedFileName or self.keywords:
            query.append('keywords=' + urllib.parse.quote(self.normalizedFileName or self.keywords))

        if self.title:
            query.append('title=' + urllib.parse.quote(self.title))

        if self.author:
            query.append('author=' + urllib.parse.quote(self.author))
        
        if not query:
            return ""
        
        return "&".join(query)

    def normalize_name(self, name) -> str:
        """
            Normalizes the album name by removing
            unwanted characters and words.
        """
        # Get name from either album or title
        logger.debug('Input Name: %s', name)

        # Remove Diacritics
        name = self.remove_diacritics(name)
        # Remove brackets and text inside
        name = re.sub(r'\[[^"]*\]', '', name)
        # Remove unwanted characters
        name = re.sub(r'[^\w\s]', '', name)
        # Remove unwanted words
        name = re.sub(r'\b(official|audiobook|unabridged|abridged)\b', '', name, flags=re.IGNORECASE)
        # Remove unwanted whitespaces
        name = re.sub(r'\s+', ' ', name)
        # Remove leading and trailing whitespaces
        name = name.strip()

        logger.debug(f'Normalized Name: {name}')

        return name

    def build_url(self, query):
        """
            Generates the URL string with search paramaters for API call.
        """
        # Setup region helper to get search URL
        region_helper = RegionTool(region=self.region, query=query)

        search_url = region_helper.get_api_search_url()

        logger.debug('Search URL: %s', search_url)

        return search_url
    
    def parse_api_response(self, api_response: dict[str, list[dict]]) -> list[dict[str, str|int]]:
        """
            Collects keys used for each item from API response,
            for Plex search results.
        """
        search_results = []
        for item in api_response['products']:
            # Only append results which have valid keys
            item: dict
            if item.keys() >= {
                "asin",
                "authors",
                "language",
                "narrators",
                "release_date",
                "title"
            }:
                search_results.append(
                    {
                        'asin': item['asin'],
                        'author': item['authors'],
                        'date': item['release_date'],
                        'language': item['language'],
                        'narrator': item['narrators'],
                        'region': self.region,
                        'title': item['title'],
                    }
                )
        return search_results

    @staticmethod
    def remove_diacritics(s):
        nkfd_form = unicodedata.normalize('NFKD', str(s))
        return u"".join([c for c in nkfd_form if not unicodedata.combining(c)])



class ScoreTool:
    # Starting value for score before deductions are taken.
    INITIAL_SCORE = 100

    def __init__(
        self,
        helper: SearchTool,
        index,
        locale,
        result_dict,
        year=None
    ):
        self.helper: SearchTool = helper
        self.index = index
        self.english_locale = locale
        self.result_dict = result_dict
        self.year = year

    def reduce_string(self, string: str):
        """
            Reduces a string to lowercase and removes
            punctuation and spaces.
        """
        logger.info(f"{string=}")
        normalized = string \
            .lower() \
            .replace("-", "") \
            .replace(' ', '') \
            .replace('.', '') \
            .replace(',', '')
        return normalized

    def run_score_book(self):
        """
            Scores a book result.
        """
        self.asin = self.result_dict['asin']
        self.authors_concat = ', '.join(
            author['name'] for author in self.result_dict['author']
        )
        self.author = self.result_dict['author'][0]['name']
        self.date = self.result_dict['date']
        self.language = self.result_dict['language'].title()
        self.narrator = self.result_dict['narrator'][0]['name']
        self.region = self.result_dict['region']
        self.title = self.result_dict['title']
        return self.score_result()

    def sum_scores(self, numberlist):
        """
            Sums a list of numbers.
        """
        # Because builtin sum() isn't available
        return reduce(
            lambda x, y: x + y, numberlist, 0
        )

    def score_create_result(self, score) -> dict[str, str]:
        """
            Creates a result dict for the score.
            Logs the score and the data used to calculate it.
        """
        data_to_logger = []
        score_dict = {}

        # Go through all the keys for the result and log as we go
        if self.asin:
            score_dict['asin'] = self.asin
            data_to_logger.append({'ASIN is': self.asin})
        if self.author:
            score_dict['author'] = self.author
            data_to_logger.append({'Author is': self.author})
        if self.date:
            score_dict['date'] = self.date
            data_to_logger.append({'Date is': self.date})
        if self.narrator:
            score_dict['narrator'] = self.narrator
            data_to_logger.append({'Narrator is': self.narrator})
        if self.region:
            score_dict['region'] = self.region
            data_to_logger.append({'Region is': self.region})
        if score is not None:
            score_dict['score'] = score
            data_to_logger.append({'Score is': str(score)})
        if self.title:
            score_dict['title'] = self.title
            data_to_logger.append({'Title is': self.title})
        if self.year:
            score_dict['year'] = self.year

        logger.info(data_to_logger)
        return score_dict

    def score_result(self):
        """
            Scores a result.
        """
        # Array to hold score points for processing
        all_scores = []

        # Album name score
        if self.title:
            title_score = self.score_album(self.title)
            if title_score:
                all_scores.append(title_score)
        # Author name score
        if self.authors_concat:
            author_score = self.score_author(self.authors_concat)
            if author_score:
                all_scores.append(author_score)
        # Library language score
        if self.language:
            lang_score = self.score_language(self.language)
            if lang_score:
                all_scores.append(lang_score)

        # Subtract difference from initial score
        # Subtract index to use Audible relevance as weight
        score = self.INITIAL_SCORE - self.sum_scores(all_scores) - self.index

        logger.info(f"Result #{self.index + 1}, Score: {score}")

        # Create result dict
        return self.score_create_result(score)
                
    def score_album(self, title: str):
        """
            Compare the input album similarity to the search result album.
            Score is calculated with LevenshteinDistance
        """
        scorebase1 = self.helper.title or self.helper.filename
        if not scorebase1:
            logger.error('No album title found in file metadata')
            return 50
        scorebase2 = title #.encode('utf-8')
        album_score = distance(
            self.reduce_string(scorebase1),
            self.reduce_string(scorebase2)
        ) * 2
        logger.debug("Score deduction from album: " + str(album_score))
        return album_score

    def score_author(self, author: str):
        """
            Compare the input author similarity to the search result author.
            Score is calculated with LevenshteinDistance
        """
        if self.helper.author:
            scorebase3 = self.helper.author
            scorebase4 = author
            author_score = distance(
                self.reduce_string(scorebase3),
                self.reduce_string(scorebase4)
            ) * 10
            logger.debug("Score deduction from author: " + str(author_score))
            return author_score

        logger.warn('No artist found in file metadata')
        return 20

    def score_language(self, language: str):
        """
            Compare the library language to search results
            and knock off 2 points if they don't match.
        """
        lang_dict = {
            self.english_locale: 'English',
            'de': 'German',
            'es': 'Spanish',
            'fr': 'French',
            'it': 'Italian',
            'ja': 'Japanese',
        }

        if language != lang_dict[settings.LANGUAGE_CODE]:
            logger.debug(
                'Audible language: %s; Library language: %s',
                language,
                lang_dict[settings.LANGUAGE_CODE]
            )
            logger.debug("Book is not library language, deduct 2 points")
            return 2
        return 0
