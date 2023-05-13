# System imports
import logging
import os
from datetime import timedelta
from pathlib import Path

import requests
from django.conf import settings
from django.contrib import messages
from django.http import HttpRequest, HttpResponseBadRequest, JsonResponse
from django.shortcuts import redirect, render
from django.views.generic import TemplateView, View
# core merge logic:
from m4b_merge import helpers

# Import Merge functions for django
from utils.merge import create_book
# Import Search tools
from utils.search_tools import ScoreTool, SearchTool

# Forms import
from .forms import SettingForm
# Models import
from .models import Book, Setting, StatusChoices
from .tasks import m4b_merge_task

# Get an instance of a logger
logger = logging.getLogger(__name__)

# If using docker, default to /input folder, else $USER/input
if Path('/input').is_dir():
    rootdir = "/input"
else:
    rootdir = f"{str(Path.home())}/input"


class ImportView(TemplateView):
    template_name = "importer.html"

    def get_context_data(self, **kwargs):
        context = {
            "contents": sorted(
                Path(rootdir).iterdir(), key=os.path.getmtime, reverse=True
            )
        }
        return context

    def post(self, request):
        # Redirect if this is a new session
        existing_settings = Setting.objects.first()
        if not existing_settings:
            logger.debug("No settings found, returning to settings page")
            messages.error(
                request, "Settings must be configured before import"
            )
            return redirect("setting")

        if not (input_dir := request.POST.getlist('input_dir')):
            messages.error(request, "You must select content to import")
            return redirect("import")

        request.session['input_dir'] = input_dir
        return redirect("match")


class MatchView(TemplateView):
    template_name = "match.html"

    def get(self, request):
        # Redirect if this is a new session
        if 'input_dir' not in request.session:
            logger.debug("No session data found, returning to import page")
            return redirect("import")

        return render(request, self.template_name, self.get_context_data())

    def get_context_data(self, **kwargs) -> dict:
        # Check if any of these inputs exist in our DB
        # If so, prepopulate their asins
        context = []
        for this_dir in self.request.session['input_dir']:
            try:
                book = Book.objects.get(src_path=f"{rootdir}/{this_dir}")
            except Book.DoesNotExist:
                context.append({'src_path': this_dir})
            else:
                context.append({'src_path': this_dir, 'asin': book.asin})

        return {"context": context}

    def post(self, request: HttpRequest):
        if 'input_dir' not in request.session:
            logger.debug("No session data found, returning to import page")
            return redirect("import")

        asin_arr = []
        for key, asin in request.POST.items():
            if key != "csrfmiddlewaretoken":

                # Check for validation errors
                errors = Book.objects.book_asin_validator(asin)
                if len(errors) > 0:
                    for k, v in errors.items():
                        messages.error(request, v)
                    return redirect("match")

                existing_settings = Setting.objects.first()
                if not existing_settings:
                    messages.error(request, "Settings not set")
                    return redirect("setting")

                # Check that asin actually returns data from audible
                try:
                    helpers.validate_asin(existing_settings.api_url, asin)
                except ValueError:
                    messages.error(request, "Bad ASIN: " + asin)
                    return redirect("match")
                else:
                    asin_arr.append(asin)

        # create objects for each book, setting their status to processing
        created_books = False
        for i, asin in enumerate(asin_arr):
            original_path = Path(
                f"{rootdir}/{request.session['input_dir'][i]}")
            input_data = helpers.get_directory(original_path)

            if not input_data:
                messages.warning(
                    request, f"No supported files in {original_path}")
                continue

            logger.info(
                f"Making models and merging files for: {request.session['input_dir'][i]}")

            book = create_book(asin, original_path)
            created_books = True

            logger.info(f"Adding book {book} to processing queue")
            m4b_merge_task.delay(asin)
        
        if created_books:
            request.session.flush()
            return redirect("books")
        else:
            return redirect("match")


class AsinSearch(View):
    def get(self, request):
        accepted_keywords = ["media_dir", "title", "author", "keywords"]

        if any(key not in accepted_keywords for key in request.GET.keys()):
            return HttpResponseBadRequest(
                f"'{', '.join(request.GET.keys() -  accepted_keywords)}' are not valid parameters. \
                Valid search parameters are {accepted_keywords}"
            )

        return self.search(
            request.GET.get("media_dir"),
            request.GET.get("title"),
            request.GET.get("author"),
            request.GET.get("keywords")
        )

    def search(self, media_dir: str = "", title: str = "", author: str = "", keywords: str = "") -> JsonResponse:
        """
            Search for an album.
        """
        # Instantiate search helper
        search_helper = SearchTool(
            filename=media_dir, title=title, author=author, keywords=keywords)

        # Call search API
        results = self.call_search_api(search_helper)

        # Write search result status to log
        if not results:
            logger.warn(
                f'No results found for query {search_helper.normalizedFileName}')
            return JsonResponse([], safe=False)

        logger.debug(
            f'Found {len(results)} result(s) for query "{search_helper.normalizedFileName}"')

        results = self.process_results(search_helper, results)

        return JsonResponse(results, safe=False)

    @staticmethod
    def process_results(helper: SearchTool, result) -> list[dict[str, str | int]]:
        """
            Process the results from the API call.
        """
        scored_results = []
        # Walk the found items and gather extended information
        logger.debug(msg="Search results")
        for index, result_dict in enumerate(result):
            score_helper = ScoreTool(
                helper, index, settings.LANGUAGE_CODE, result_dict)
            scored_results.append(score_helper.run_score_book())

            # Print separators for easy reading
            if index <= len(result):
                logger.debug("-" * 35)

        return sorted(scored_results, key=lambda inf: inf['score'], reverse=True)

    @staticmethod
    def call_search_api(helper: SearchTool):
        '''
            Builds URL then calls API, returns the JSON to helper function.
        '''
        query = helper.build_search_args()
        search_url = helper.build_url(query)
        request = requests.get(search_url)
        return helper.parse_api_response(request.json())


class BookListView(TemplateView):
    template_name = "book_tabs.html"

    def get(self, request):
        done_books = Book.objects.filter(status__status=StatusChoices.DONE).order_by(
            '-created_at')
        processing_books = Book.objects.filter(
            status__status=StatusChoices.PROCESSING).order_by(
            '-created_at')
        error_books = Book.objects.filter(status__status=StatusChoices.ERROR).order_by(
            '-created_at')

        return render(request, self.template_name, self.get_context_data(
            done_books=done_books, processing_books=processing_books, error_books=error_books))

    def get_context_data(self, **kwargs) -> dict:
        context = {"default_view": "done"}

        redirect_url = self.request.META.get('HTTP_REFERER', '')
        if redirect_url.rsplit('/', 1)[1] == "match":
            context.update({"default_view": "processing"})

        for key, books in filter(lambda item: 'books' in item[0], kwargs.items()):
            context.update(
                {key: list(zip(books, self.calcBookLength(list(books))))})

        return context

    def calcBookLength(self, books: list[Book]) -> list[str]:
        # Calculate time object into sentence
        length_arr = []
        for book in books:
            d = int(
                timedelta(
                    minutes=book.runtime_length_minutes
                ).total_seconds()
            )
            book_length_calc = (
                f'{d//3600} hrs and {(d//60)%60} minutes'
            )
            length_arr.append(book_length_calc)
        return length_arr


class SettingView(TemplateView):
    template_name = "setting.html"

    def get_context_data(self, **kwargs):
        existing_settings = Setting.objects.first()
        default_data = {
            'api_url': 'https://api.audnex.us',
            'completed_directory': '/input/done',
            'input_directory': '/input',
            'num_cpus': 0,
            'output_directory': '/output',
            'output_scheme': 'author/title/title - subtitle'
        }
        if existing_settings:
            form = SettingForm(instance=existing_settings)
        else:
            form = SettingForm(initial=default_data)
        all_settings = Setting.objects.first()

        context = {
            "form": form,
            "settings": all_settings,
        }
        return context

    def post(self, request):
        existing_settings = Setting.objects.first()

        form = SettingForm(request.POST)
        if form.is_valid():
            paths_to_check = [
                'completed_directory',
                'input_directory',
                'output_directory'
            ]
            form_data = form.cleaned_data

            # Check file path validity
            for path in paths_to_check:
                errors = Setting.objects.file_path_validator(form_data[path])
                if len(errors) > 0:
                    for k, v in errors.items():
                        messages.error(request, v)
                    return redirect("setting")
            if not existing_settings:
                settings = Setting.objects.create(
                    api_url=form_data['api_url'],
                    completed_directory=form_data['completed_directory'],
                    input_directory=form_data['input_directory'],
                    num_cpus=form_data['num_cpus'],
                    output_directory=form_data['output_directory'],
                    output_scheme=form_data['output_scheme']
                )
                settings.save()
            else:
                es = existing_settings
                es.api_url = form_data['api_url']
                es.completed_directory = form_data['completed_directory']
                es.input_directory = form_data['input_directory']
                es.num_cpus = form_data['num_cpus']
                es.output_directory = form_data['output_directory']
                es.output_scheme = form_data['output_scheme']
                es.save()

            return redirect("import")

        messages.error(request, "Form is invalid")
        return redirect("setting")
