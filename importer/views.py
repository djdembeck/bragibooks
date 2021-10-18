from django.contrib import messages
from django.shortcuts import render, redirect
from django.views.generic import TemplateView
# System imports
from pathlib import Path
import logging
import os
# Models import
from .models import Book
# core merge logic:
from m4b_merge import helpers
# Import Merge functions for django
from utils.merge import Merge
# To display book length
from datetime import timedelta

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
        folder_arr = []
        for path in sorted(
            Path(rootdir).iterdir(),
            key=os.path.getmtime,
            reverse=True
        ):
            base = os.path.basename(path)
            folder_arr.append(base)

        context = {
            "this_dir": folder_arr,
        }
        return context

    def post(self, request):
        request.session['input_dir'] = request.POST.getlist('input_dir')
        return redirect("match")


class MatchView(TemplateView):
    template_name = "match.html"

    def get(self, request):
        # Redirect if this is a new session
        if 'input_dir' not in request.session:
            logger.warning(
                "No session data found, "
                "returning to import page"
            )
            return redirect("home")
        return render(request, self.template_name, self.get_context_data())

    def get_context_data(self, **kwargs):
        # Check if any of these inputs exist in our DB
        # If so, prepopulate their asins
        context_item = []
        for this_dir in self.request.session['input_dir']:
            try:
                book = Book.objects.get(src_path=f"{rootdir}/{this_dir}")
            except Book.DoesNotExist:
                context_item.append({'src_path': this_dir})
            else:
                context_item.append({'src_path': this_dir, 'asin': book.asin})

        context = {
            "this_input": context_item
        }

        return context

    def post(self, request):
        if 'input_dir' not in request.session:
            logger.warning(
                "No session data found, "
                "returning to import page"
            )
            return redirect("home")

        asin_arr = []
        dict1 = request.POST
        for key, value in dict1.items():
            if key != "csrfmiddlewaretoken":
                asin = value
                # Check for validation errors
                errors = Book.objects.book_asin_validator(asin)
                if len(errors) > 0:
                    for k, v in errors.items():
                        messages.error(request, v)
                    return redirect("match")
                # Check that asin actually returns data from audible
                try:
                    helpers.validate_asin(asin)
                except ValueError:
                    messages.error(request, "Bad ASIN: " + asin)
                    return redirect("match")
                else:
                    asin_arr.append(asin)

        for i, item in enumerate(asin_arr):
            original_path = f"{rootdir}/{request.session['input_dir'][i]}"
            input_data = helpers.get_directory(
                Path(original_path)
            )
            if input_data:
                logger.info(
                    f"Making models and merging files for: "
                    f"{request.session['input_dir'][i]}"
                )
                # Create Merge class object
                merge_object = Merge(
                    asin_arr[i], input_data, original_path
                )
                # Run merge function for the object
                merge_object.run_m4b_merge()
            else:
                messages.error(
                    request, f"No supported files in {original_path}"
                )
                return redirect("match")

        request.session['asins'] = asin_arr
        return redirect("finish")


class FinishView(TemplateView):
    template_name = "finish.html"

    def get(self, request):
        # Redirect if this is a new session
        if 'asins' not in request.session:
            logger.warning(
                "No session data found, "
                "returning to import page"
            )
            return redirect("home")
        return render(request, self.template_name, self.get_context_data())

    def get_context_data(self, **kwargs):
        asins = self.request.session['asins']
        this_book = Book.objects.filter(asin__in=asins)

        # Calculate time object into sentence
        length_arr = []
        for book in this_book:
            d = int(
                timedelta(
                    minutes=book.runtime_length_minutes
                ).total_seconds()
            )
            book_length_calc = (
                f'{d//3600} hrs and {(d//60)%60} minutes'
            )
            length_arr.append(book_length_calc)

        context = {
            "finished_books": zip(this_book, length_arr),
        }

        # Clear this session on finish
        self.request.session.flush()

        return context
