from django.shortcuts import render, redirect
import importlib, json, os, requests
from pathlib import Path
# from login.models import User
from .models import Book, Author, Narrator, Genre
# core merge logic:
from .merge_cli import *
from django.contrib import messages
# To display book length
from datetime import timedelta

# If using docker, default to /input folder, else $USER/input
if Path('/input').is_dir():
	rootdir = Path('/input')
else:
	rootdir = f"{str(Path.home())}/input"

def importer(request):
	folder_arr = []
	for path in sorted(Path(rootdir).iterdir(), key=os.path.getmtime, reverse=True):
		base = os.path.basename(path)
		folder_arr.append(base)

	context = {
		"this_dir": folder_arr,
	}
	return render(request, "importer.html", context)

def dir_selection(request):
	request.session['input_dir'] = request.POST['input_dir']

	check_book_path = Book.objects.filter(src_path__icontains=f"{rootdir}/{request.session['input_dir']}")
	if check_book_path:
		confirmed_book = Book.objects.get(src_path__icontains=f"{rootdir}/{request.session['input_dir']}")
		asin = confirmed_book.asin
		return redirect(f'/import/{asin}/confirm')
	else:
		return redirect('/import/match')

def match(request):
	context = {
		"this_input": request.session['input_dir']
	}
	
	return render(request, "match.html", context)

def make_models(asin, input_data):
	
	metadata = audible_parser(asin)
	m4b_data(input_data, metadata, output)

	## Book DB entry
	if 'subtitle' in metadata:
		base_title = metadata['title']
		base_subtitle = metadata['subtitle']
		title = f"{base_title} - {base_subtitle}"
	else:
		title = metadata['title']

	new_book = Book.objects.create(
		title=metadata['title'],
		asin=asin,
		short_desc=metadata['short_summary'],
		long_desc=metadata['long_summary'],
		release_date=metadata['release_date'],
		publisher=metadata['publisher_name'],
		lang=metadata['language'],
		runtime_length_minutes=metadata['runtime_length_min'],
		format_type=metadata['format_type'],
		converted=True,
		src_path=input_data[0],
		dest_path=(
		f"\""
		f"{output}/"
		f"{metadata['authors'][0]}/"
		f"{metadata['title']}/"
		f"{title}.m4b"
		f"\""
		)
		)

	# Only add in series if it exists
	if 'series' in metadata:
		new_book.series = metadata['series']

	new_book.save()

	## Author DB entry
	# Create new entry for each author if there's more than one
	if len(metadata['authors']) > 1:
		for author in metadata['authors']:
			author_name_split = author['name'].split()
			last_name_index = len(author_name_split) - 1
			if not Author.objects.filter(
				asin=author['asin']
			):
				new_author = Author.objects.create(
					asin=author['asin'],
					first_name=author_name_split[0],
					last_name=author_name_split[last_name_index]
				)
				new_author.books.add(new_book)
				new_author.save()
			else:
				existing_author = Author.objects.get(
					asin=author['asin']
				)
				existing_author.books.add(new_book)
				existing_author.save()
	else:
		author_name_split = metadata['authors'][0]['name'].split()
		last_name_index = len(author_name_split) - 1
		print(author_name_split[last_name_index])
		if not Author.objects.filter(
			asin=metadata['authors'][0]['asin']
		):
			new_author = Author.objects.create(
				asin=metadata['authors'][0]['asin'],
				first_name=author_name_split[0],
				last_name=author_name_split[last_name_index]
			)
			new_author.books.add(new_book)
			new_author.save()
		else:
			existing_author = Author.objects.get(
				asin=metadata['authors'][0]['asin']
			)
			existing_author.books.add(new_book)
			existing_author.save()

	## Narrator DB entry
	# Create new entry for each narrator if there's more than one
	if len(metadata['narrators']) > 1:
		for narrator in metadata['narrators']:
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
				new_narrator.books.add(new_book)
				new_narrator.save()
			else:
				existing_narrator = Narrator.objects.get(
					first_name=narr_name_split[0],
					last_name=narr_name_split[last_name_index]
				)
				existing_narrator.books.add(new_book)
				existing_narrator.save()
	else:
		narr_name_split = metadata['narrators'][0].split()
		last_name_index = len(narr_name_split) - 1
		if not Narrator.objects.filter(
			first_name=narr_name_split[0],
			last_name=narr_name_split[last_name_index]
		):
			new_narrator = Narrator.objects.create(
				first_name=narr_name_split[0],
				last_name=narr_name_split[last_name_index]
			)
			new_narrator.books.add(new_book)
			new_narrator.save()
		else:
			existing_narrator = Narrator.objects.get(
				first_name=narr_name_split[0],
				last_name=narr_name_split[last_name_index]
			)
			existing_narrator.books.add(new_book)
			existing_narrator.save()
		
def api_auth(request):
	return render(request, "authenticate.html")

def get_auth(request):
	audible_login(
		USERNAME=request.POST['aud_email'],
		PASSWORD=request.POST['aud_pass'])
	return redirect('/import/match')

def get_asin(request):
	#check that user is signed into audible api
	auth_file = Path(dir_path, ".aud_auth.txt")
	if not auth_file.exists():
		return redirect('/import/api_auth')

	asin = request.POST['asin']
	input_data = get_directory(
		f"{rootdir}/{request.session['input_dir']}"
		)
	# Check for validation errors
	errors = Book.objects.book_asin_validator(request.POST)
	if len(errors) > 0:
		for k, v in errors.items():
			messages.error(request, v)
		return redirect('/import/match')
	else:
		# Check that asin actually returns data from audible
		check = requests.get(f"https://www.audible.com/pd/{asin}")
		if check.status_code == 200:
			make_models(asin, input_data)
			return redirect(f'/import/{asin}/confirm')
		else:
			print(f'Got http error: {check.status_code}')
			return redirect('/import/match')

def finish(request, asin):
	this_book = Book.objects.get(asin=asin)
	d = timedelta(minutes=this_book.runtime_length_minutes)
	book_length_calc = (
		f'{d.seconds//3600} hrs and {(d.seconds//60)%60} minutes'
	)

	context = {
		"this_book": this_book,
		"book_length": book_length_calc
	}
	
	return render(request, "finish.html", context)