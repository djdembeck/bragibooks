from django.shortcuts import render, redirect
import importlib, json, os, requests
from pathlib import Path
# from login.models import User
from .models import Book, Author, Narrator, Genre
from .merge_cli import *
from django.contrib import messages

rootdir = ''

def importer(request):
	folder_arr = []
	for path in Path(rootdir).iterdir():
		if path.is_dir():
			base = os.path.basename(path)
			full = path
			folder_arr.append(base)

	context = {
		"this_dir": folder_arr,
	}
	return render(request, "importer.html", context)

def dir_selection(request):
	request.session['input_dir'] = request.POST['input_dir']

	return redirect('/import/match')

def match(request):
	context = {
		"this_input": request.session['input_dir']
	}
	
	return render(request, "match.html", context)

def make_models(asin, input_data):
	
	metadata = audible_parser(asin)
	m4b_data(input_data, metadata, output)

	# Book DB entry
	#TODO: fix long_desc
	if 'subtitle' in metadata:
		m.title = metadata['title']
		m.subtitle = metadata['subtitle']
		title = f'{m.title} - {m.subtitle}'
	else:
		title = metadata['title']

	new_book = Book.objects.create(title=metadata['title'], asin=asin, short_desc=metadata['summary'], long_desc="", release_date=metadata['release_date'], series=metadata['series'], converted=True, src_path=input_data[0], dest_path=f"\"{output}/{metadata['authors'][0]}/{metadata['title']}/{title}.m4b\"")
	new_book.save()

	# # Author DB entry
	# # Need check if author exists
	# new_author = Author.objects.create(short_desc="", long_desc="")
	# new_author.save()

	# # Need check if narrator exists
	# # Narrator DB entry
	# new_narrator = Narrator.objects.create()
	# new_narrator.save()

def get_asin(request):
	asin = request.POST['asin']
	input_data = get_directory(f"{rootdir}/{request.session['input_dir']}")
	check = requests.get(f"https://www.audible.com/pd/{asin}")
	if check.status_code == 200:
		errors = Book.objects.asin_validator(request.POST)
		print("great success")
		if len(errors) > 0:
			for k, v in errors.items():
				messages.error(request, v)
			return redirect('/import/match')
		else:
			make_models(asin, input_data)
			return redirect(f'/import/{asin}/confirm')
	else:
		print(f'Got http error: {check.status_code}')
		return redirect('/import/match')

def finish(request, asin):
	context = {
		"this_book": Book.objects.get(asin=asin)
	}
	
	return render(request, "finish.html", context)