from django.db import models

class BookManager(models.Manager):
	def asin_validator(self, post_data):
		errors = {}

		if Book.objects.filter(asin=post_data['asin']):
			errors["dupe_asin"] = "A book with that ASIN already exists"

		return errors

class Book(models.Model):
	title = models.CharField(max_length=255)
	asin = models.CharField(max_length=10)
	short_desc = models.TextField()
	long_desc = models.TextField()
	release_date = models.DateField()
	series = models.CharField(max_length=255)
	# genre = models.TextField()
	# publisher
	# lang
	# length_minutes = 
	# format_type = 
	converted = models.BooleanField()
	src_path = models.FilePathField()
	dest_path = models.FilePathField()
	created_at = models.DateTimeField(auto_now_add=True)
	updated_at = models.DateTimeField(auto_now=True)
	objects = BookManager()

class Author(models.Model):
	first_name = models.CharField(max_length=45)
	last_name = models.CharField(max_length=45)
	asin = models.CharField(max_length=10)
	books = models.ManyToManyField(Book, related_name="authors")
	short_desc = models.TextField()
	long_desc = models.TextField()
	created_at = models.DateTimeField(auto_now_add=True)
	updated_at = models.DateTimeField(auto_now=True)

class Narrator(models.Model):
	first_name = models.CharField(max_length=45)
	last_name = models.CharField(max_length=45)
	asin = models.CharField(max_length=10)
	books = models.ManyToManyField(Book, related_name="narrators")
	short_desc = models.TextField()
	long_desc = models.TextField()
	created_at = models.DateTimeField(auto_now_add=True)
	updated_at = models.DateTimeField(auto_now=True)

class Genre(models.Model):
	name = models.CharField(max_length=255)
	asin = models.CharField(max_length=10)
	books = models.ManyToManyField(Book, related_name="genres")
	authors = models.ManyToManyField(Author, related_name="genres")
	narrators = models.ManyToManyField(Narrator, related_name="genres")
	created_at = models.DateTimeField(auto_now_add=True)
	updated_at = models.DateTimeField(auto_now=True)