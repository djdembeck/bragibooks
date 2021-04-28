from pathlib import Path
import audible, collections, html2text, getpass, os, readline
from datetime import datetime

dir_path = Path(__file__).resolve().parent

def audible_login():
	print("You need to login")
	USERNAME = input("Email: ")
	PASSWORD = getpass.getpass()
	auth = audible.Authenticator.from_login(
		USERNAME,
		PASSWORD,
		locale="us",
		with_username=False,
		register=True
	)
	auth.to_file(Path(dir_path, ".aud_auth.txt"))

def audible_parser(asin):
	auth_file = Path(dir_path, ".aud_auth.txt")
	# Only proceed auth exists
	#TODO: add functional logged in state checking
	if auth_file.exists():
		auth = audible.Authenticator.from_file(auth_file)
		client = audible.Client(auth)
		print("Already logged in")
		ASIN = asin
		aud_json = client.get(
			f"catalog/products/{ASIN}",
			params={
				"response_groups": f'contributors,product_desc,product_extended_attrs,product_attrs',
				"asins": ASIN
			}
		)

		### JSON RESPONSE
		# We have:
		# Summary, Title, Author, Narrator
		# Want: series number

		# metadata dictionary
		metadata_dict = {}

		## Title
		# Use subtitle if it exists
		if 'subtitle' in aud_json['product']:
			aud_title_start = aud_json['product']['title']
			aud_title_end = aud_json['product']['subtitle']
			metadata_dict['title'] = f"{aud_title_start} - {aud_title_end}"
		else:
			metadata_dict['title'] = aud_json['product']['title']

		## Summary
		aud_summary_json = aud_json['product']['merchandising_summary']
		metadata_dict['summary'] = html2text.html2text(aud_summary_json)

		## Authors
		aud_authors_json = aud_json['product']['authors']
		# check if list contains more than 1 author
		if len(aud_authors_json) > 1:
			aud_authors_arr = []
			for narrator in range(len(aud_authors_json)):
				# from array of dicts, get author name
				aud_authors_arr.append(aud_authors_json[narrator]['name'])
			# aud_authors = ', '.join(aud_authors_arr)
			metadata_dict['authors'] = aud_authors_arr
		else:
			# else author name will be in first element dict
			metadata_dict['authors'] = aud_authors_json[0]['name']
		
		## Narrators
		aud_narrators_json = aud_json['product']['narrators']
		# check if list contains more than 1 narrator
		if len(aud_narrators_json) > 1:
			aud_narrators_arr = []
			for narrator in range(len(aud_narrators_json)):
				# from array of dicts, get narrator name
				aud_narrators_arr.append(aud_narrators_json[narrator]['name'])
			# aud_narrators = ', '.join(aud_narrators_arr)
			metadata_dict['narrators'] = aud_narrators_arr
		else:
			# else narrator name will be in first element dict
			metadata_dict['narrators'] = aud_narrators_json[0]['name']

		## Series
		# Check if book has publication name (series)
		if 'publication_name' in aud_json['product']:
			metadata_dict['series'] = aud_json['product']['publication_name']

		## Release date
		if 'release_date' in aud_json['product']:
			# Convert date string into datetime object
			metadata_dict['release_date'] = datetime.strptime(aud_json['product']['release_date'], '%Y-%m-%d').date()
		
		# return all data
		return metadata_dict
	else:
		print("no auth")
		# If no auth file exists, call login function
		audible_login()

def get_directory():
	# enable using tab for filename completion
	readline.set_completer_delims(' \t\n=')
	readline.parse_and_bind("tab: complete")

	# get dir from user
	input_take = input("Enter directory to use: ")

	for dirpath, dirnames, files in os.walk(input_take):
		EXTENSIONS=['mp3', 'm4a', 'm4b']

		for EXT in EXTENSIONS:
			if collections.Counter(p.suffix for p in Path(dirpath).resolve().glob(f'*.{EXT}')):
				USE_EXT = EXT
	return Path(dirpath), USE_EXT

def call():
	print(get_directory())
	asin = input("Audiobook ASIN: ")
	print(audible_parser(asin))

call()