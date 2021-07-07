from django.db import models


class BookManager(models.Manager):
    def book_asin_validator(self, asin):
        errors = {}

        if Book.objects.filter(asin=asin):
            errors["dupe_asin"] = f"A book with the ASIN {asin} already exists"

        if len(asin) != 10 and len(asin) != 0:
            errors['invalid_asin'] = f"Invalid ASIN format for {asin}"

        if len(asin) == 0:
            errors['blank_asin'] = "Must fill in all ASIN fields"

        return errors


class Book(models.Model):
    title = models.CharField(max_length=255)
    asin = models.CharField(max_length=10)
    short_desc = models.TextField()
    long_desc = models.TextField()
    release_date = models.DateField()
    series = models.CharField(max_length=255, blank=True, default='')
    # genre = models.TextField()
    publisher = models.CharField(max_length=255)
    lang = models.CharField(max_length=25)
    runtime_length_minutes = models.IntegerField()
    format_type = models.CharField(max_length=25)
    converted = models.BooleanField()
    src_path = models.FilePathField()
    dest_path = models.FilePathField()
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)
    objects = BookManager()


class Author(models.Model):
    first_name = models.CharField(max_length=45)
    last_name = models.CharField(max_length=45)
    asin = models.CharField(max_length=10, null=True, default='')
    books = models.ManyToManyField(Book, related_name="authors")
    short_desc = models.TextField(blank=True, default='')
    long_desc = models.TextField(blank=True, default='')
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)


class Narrator(models.Model):
    first_name = models.CharField(max_length=45)
    last_name = models.CharField(max_length=45)
    books = models.ManyToManyField(Book, related_name="narrators")
    short_desc = models.TextField(blank=True, default='')
    long_desc = models.TextField(blank=True, default='')
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
