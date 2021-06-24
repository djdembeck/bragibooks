from pathlib import Path
import argparse, audible, collections, getpass, \
	html2text, logging, math, os, shutil
from datetime import datetime
from pathvalidate import sanitize_filename
from pydub.utils import mediainfo

### User editable variables
# for non-default m4b-tool install path
m4bpath = "m4b-tool"

# output dir
# leaving blank uses /output for docker or $USER/output for anything else
output = ""

# Number of cpus to use for jobs
cpus_to_use = ""
###

# config section for docker
if Path('/config').is_dir():
	dir_path = Path('/config')
else:
	dir_path = Path(f"{Path(__file__).resolve().parent.parent}/config")
	Path(dir_path).mkdir(
	parents=True,
	exist_ok=True
	)

def audible_login(USERNAME="", PASSWORD=""):
	print("You need to login")
	# Check if we're coming from web or not
	if not USERNAME:
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

	auth = audible.Authenticator.from_file(auth_file)
	client = audible.Client(auth)
	ASIN = asin
	aud_json = client.get(
		f"catalog/products/{ASIN}",
		params={
			"response_groups": f'''
			contributors,
			product_desc,
			product_extended_attrs,
			product_attrs''',
			"asins": ASIN
		}
	)

	### JSON RESPONSE
	# We have:
	# Summary, Title, Author, Narrator, Series
	# Want: series number

	# metadata dictionary
	metadata_dict = {}

	## Title
	# Use subtitle if it exists
	if 'subtitle' in aud_json['product']:
		aud_title_start = aud_json['product']['title']
		aud_title_end = aud_json['product']['subtitle']
		metadata_dict['title'] = aud_title_start
		metadata_dict['subtitle'] = aud_title_end
	else:
		metadata_dict['title'] = (
			aud_json['product']['title']
			)

	## Short summary
	aud_short_summary_json = (
		aud_json['product']['merchandising_summary']
		)
	metadata_dict['short_summary'] = (
		html2text.html2text(aud_short_summary_json).replace("\n", " ")
		)

	## Long summary
	aud_long_summary_json = (
		aud_json['product']['publisher_summary']
		)
	metadata_dict['long_summary'] = aud_long_summary_json

	## Authors
	aud_authors_json = (
		aud_json['product']['authors']
		)
	# check if list contains more than 1 author
	if len(aud_authors_json) > 1:
		aud_authors_arr = []
		for author in range(len(aud_authors_json)):
			# Use ASIN for author only if available
			if aud_authors_json[author].get('asin'):
				# from array of dicts, get author name
				aud_authors_arr.append(
				{
				'asin': aud_authors_json[author]['asin'],
				'name': aud_authors_json[author]['name']
				}
					)
			else:
				aud_authors_arr.append(
				{
				'name': aud_authors_json[author]['name']
				}
					)
		metadata_dict['authors'] = aud_authors_arr
	else:
		# else author name will be in first element dict
		# Use ASIN for author only if available
		if aud_authors_json[0].get('asin'):
			metadata_dict['authors'] = [
				{
				'asin': aud_authors_json[0]['asin'],
				'name': aud_authors_json[0]['name']
				}
			]
		else:
			metadata_dict['authors'] = [
				{
				'name': aud_authors_json[0]['name']
				}
			]
	
	## Narrators
	aud_narrators_json = (
		aud_json['product']['narrators']
		)
	# check if list contains more than 1 narrator
	if len(aud_narrators_json) > 1:
		aud_narrators_arr = []
		for narrator in range(len(aud_narrators_json)):
			# from array of dicts, get narrator name
			aud_narrators_arr.append(
				aud_narrators_json[narrator]['name']
				)
		metadata_dict['narrators'] = aud_narrators_arr
	else:
		# else narrator name will be in first element dict
		metadata_dict['narrators'] = (
			[aud_narrators_json[0]['name']]
			)

	## Series
	# Check if book has publication name (series)
	if 'publication_name' in aud_json['product']:
		metadata_dict['series'] = (
			aud_json['product']['publication_name']
			)

	## Release date
	if 'release_date' in aud_json['product']:
		# Convert date string into datetime object
		metadata_dict['release_date'] = (
			datetime.strptime(
				aud_json['product']['release_date'], '%Y-%m-%d'
				).date()
			)

	## Publisher
	if 'publisher_name' in aud_json['product']:
		metadata_dict['publisher_name'] = aud_json['product']['publisher_name']

	## Language
	if 'language' in aud_json['product']:
		metadata_dict['language'] = aud_json['product']['language']

	## Runtime in minutes
	if 'runtime_length_min' in aud_json['product']:
		metadata_dict['runtime_length_min'] = aud_json['product']['runtime_length_min']

	## Format type (abridged or unabridged)
	if 'format_type' in aud_json['product']:
		metadata_dict['format_type'] = aud_json['product']['format_type']

	# return all data
	return metadata_dict

def get_directory(input_take):
	# Check if input is a dir
	if Path(input_take).is_dir():
		# Check if input has multiple subdirs
		num_of_subdirs = len(next(os.walk(input_take))[1])
		if num_of_subdirs >= 1:
			logging.info(
				f"Found multiple ({num_of_subdirs}) subdirs, "
					f"using those as input (multi-disc)"
				)
			dirpath = inputs
			USE_EXT = None
			num_of_files = num_of_subdirs
		else:
			for dirpath, dirnames, files in os.walk(input_take):
				EXTENSIONS=['mp3', 'm4a', 'm4b']

				for EXT in EXTENSIONS:
					if collections.Counter(
						p.suffix for p in Path(dirpath)\
							.resolve().glob(f'*.{EXT}')
						):
						USE_EXT = EXT
						list_of_files = os.listdir(Path(dirpath))
						# Case for single file in a folder
						if sum(
							x.endswith(f'.{USE_EXT}') 
							for x in list_of_files
							) == 1:
							for m4b_file in Path(dirpath).glob(f'*.{USE_EXT}'):
								dirpath = m4b_file
							num_of_files = 1
						else:
							num_of_files = sum(
								x.endswith(f'.{USE_EXT}') 
								for x in list_of_files
								)
	# Check if input is a file
	elif Path(input_take).is_file():
		dirpath = Path(input_take)
		USE_EXT_PRE = dirpath.suffix
		USE_EXT = Path(USE_EXT_PRE).stem.split('.')[1]
		num_of_files = 1

	return dirpath, USE_EXT, num_of_files

def m4b_data(input_data, metadata, output):
	## Checks
	# Find path to m4b-tool binary
	m4b_tool = shutil.which(m4bpath)

	# Check that binary actually exists
	if not m4b_tool:
		# try to automatically recover
		if shutil.which('m4b-tool'):
			m4b_tool = shutil.which('m4b-tool')
		else:
			raise SystemExit(
				'Error: Cannot find m4b-tool binary.'
				)
	# If no response from binary, exit
	if not m4b_tool:
		raise SystemExit(
			'Error: Could not successfully run m4b-tool, exiting.'
			)

	# Setup output folder defaults
	if not output:
		# If using docker, default to /input folder, else $USER/input
		if Path('/output').is_dir():
			logging.info("Defaulting output to docker directory")
			output = Path('/output')
		else:
			logging.info("Defaulting output to home directory")
			default_output = Path.home()
			output = Path(f"{default_output}/output")

	## Metadata variables
	# Only use subtitle in case of metadata, not file name
	if 'subtitle' in metadata:
		base_title = metadata['title']
		base_subtitle = metadata['subtitle']
		title = f"{base_title} - {base_subtitle}"
	else:
		title = metadata['title']
	# Only use first author/narrator for file names;
	# no subtitle for file name
	path_title = metadata['title']
	path_author = metadata['authors'][0]['name']
	path_narrator = metadata['narrators'][0]
	# For embedded, use all authors/narrators
	author_name_arr = []
	for authors in metadata['authors']:
		author_name_arr.append(authors['name'])
	author = ', '.join(author_name_arr)
	narrator = ', '.join(metadata['narrators'])
	if 'series' in metadata:
		series = metadata['series']
	else:
		series = None
	summary = metadata['short_summary']
	year = metadata['release_date'].year

	book_output = (
		f"{output}/{sanitize_filename(path_author)}/{sanitize_filename(path_title)}"
		)
	file_title = sanitize_filename(title)
	##

	## File variables
	in_dir = input_data[0]
	in_ext = input_data[1]
	num_of_files = input_data[2]
	##

	# Available CPU cores to use
	if not cpus_to_use:
		num_cpus = os.cpu_count()
	else:
		num_cpus = cpus_to_use

	# Make necessary directories
	Path(book_output).mkdir(
		parents=True,
		exist_ok=True
		)

	## Array for argument use
	# args for multiple input files in a folder
	if Path(in_dir).is_dir() and num_of_files > 1 or in_ext == None:
		logging.info("Got multiple files in a dir")

		# Find first file with our extension, to check rates against
		first_file_index = 0
		while True:
			if Path(sorted(os.listdir(in_dir))[first_file_index]).suffix == f".{in_ext}":
				break
			first_file_index += 1
		first_file = sorted(os.listdir(in_dir))[first_file_index]

		## Mediainfo data
		# Divide bitrate by 1k, round up, and return back to 1k divisible for round number.
		target_bitrate = math.ceil(
			int(mediainfo(f"{in_dir}/{first_file}")['bit_rate']) / 1000
		) * 1000

		target_samplerate =	int(mediainfo(f"{in_dir}/{first_file}")['sample_rate'])

		logging.info(f"Source bitrate: {target_bitrate}")
		logging.info(f"Source samplerate: {target_samplerate}")
		##
		args = [
			' merge',
			f"--output-file=\"{book_output}/{file_title}.m4b\"",
			f"--name=\"{title}\"",
			f"--album=\"{path_title}\"",
			f"--artist=\"{narrator}\"",
			f"--albumartist=\"{author}\"",
			f"--year=\"{year}\"",
			f"--description=\"{summary}\"",
			f"--audio-bitrate=\"{target_bitrate}\"",
			f"--audio-samplerate=\"{target_samplerate}\"",
			'--force',
			'--no-chapter-reindexing',
			'--no-cleanup',
			f'--jobs={num_cpus}'
		]
		if series:
			args.append(f'--series \"{series}\"')

		# m4b command with passed args
		m4b_cmd = (
			m4b_tool + 
		' '.join(args) + 
		f" \"{in_dir}\""
		)
		os.system(m4b_cmd)

		m4b_fix_chapters(
			f"{book_output}/{file_title}.chapters.txt",
			f"{book_output}/{file_title}.m4b",
			m4b_tool
			)
		
	# args for single m4b input file
	elif Path(in_dir).is_file() and in_ext == "m4b":
		## Mediainfo data
		# Divide bitrate by 1k, round up, and return back to 1k divisible for round number.
		target_bitrate = math.ceil(
			int(mediainfo(in_dir)['bit_rate']) / 1000
		) * 1000

		target_samplerate =	int(mediainfo(in_dir)['sample_rate'])
		##

		logging.info("got single m4b input")
		m4b_cmd = (
			m4b_tool + 
		' meta ' + 
		f'--export-chapters=\"\"' + 
		f" \"{in_dir}\""
		)
		os.system(m4b_cmd)
		shutil.move(
			f"{in_dir.parent}/{in_dir.stem}.chapters.txt",
			f"{book_output}/{file_title}.chapters.txt"
			)

		args = [
			' meta',
			f"--name=\"{title}\"",
			f"--album=\"{path_title}\"",
			f"--artist=\"{narrator}\"",
			f"--albumartist=\"{author}\"",
			f"--year=\"{year}\"",
			f"--description=\"{summary}\""
		]
		if series:
			args.append(f"--series \"{series}\"")

		# make backup file
		shutil.copy(
			in_dir,
			f"{in_dir.parent}/{in_dir.stem}.new.m4b"
			)

		# m4b command with passed args
		m4b_cmd = (
			m4b_tool + 
			' '.join(args) + 
			f" \"{in_dir.parent}/{in_dir.stem}.new.m4b\"")
		os.system(m4b_cmd)

		# Move completed file
		shutil.move(
			f"{in_dir.parent}/{in_dir.stem}.new.m4b",
			f"{book_output}/{file_title}.m4b"
			)

		m4b_fix_chapters(
			f"{book_output}/{file_title}.chapters.txt",
			f"{book_output}/{file_title}.m4b",
			m4b_tool
			)

	elif not in_ext:
		logging.error(f"No recognized filetypes found for {title}")

def m4b_fix_chapters(input, target, m4b_tool):
	new_file_content = ""
	with open(input) as f:
		# Store and then skip past total length section
		for line in f:
			if "# total-length" in line.strip():
				new_file_content += line.strip() +"\n"
				break
		# Iterate over rest of the file
		counter = 0
		for line in f:
			stripped_line = line.strip()
			counter += 1
			new_line = (
				(stripped_line[0:13]) + 
				f'Chapter {"{0:0=2d}".format(counter)}'
				)
			new_file_content += new_line +"\n"

	with open(input, 'w') as f:
		f.write(new_file_content)
	
	# Apply fixed chapters to file
	m4b_chap_cmd = (
		m4b_tool + 
		' meta ' + 
		f" \"{target}\" " + 
		f"--import-chapters=\"{input}\""
		)
	os.system(m4b_chap_cmd)

def call(inputs):
	auth_file = Path(dir_path, ".aud_auth.txt")
	# Only proceed if auth exists
	if not auth_file.exists():
		logging.error("Not logged in to Audible")
		# If no auth file exists, call login function
		audible_login()

	print(f"Working on: {inputs}")
	input_data = get_directory(inputs)
	asin = input("Audiobook ASIN: ")
	metadata = audible_parser(asin)
	m4b_data(input_data, metadata, output)

# Only run call if using CLI directly
if __name__ == "__main__":
	parser = argparse.ArgumentParser(
		description='Bragi Books merge cli'
		)
	parser.add_argument(
		"-i", "--inputs",
		help="Input paths to process",
		nargs='+',
		required=True,
		type=Path
		)
	parser.add_argument(
		"--log_level",
		help="Set logging level"
		)
	args = parser.parse_args()

	# Get log level from system or input
	if args.log_level:
		numeric_level = getattr(logging, args.log_level.upper(), None)
		if not isinstance(numeric_level, int):
			raise ValueError('Invalid log level: %s' % log_level)
		logging.basicConfig(level=numeric_level)
	else:
		logging.basicConfig(level=os.environ.get("LOGLEVEL", "INFO"))
	
	# Run through inputs
	for inputs in args.inputs:
		call(inputs)