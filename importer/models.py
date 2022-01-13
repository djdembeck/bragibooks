from django.db import models
from pathlib import Path


class BookManager(models.Manager):
    def book_asin_validator(self, asin):
        errors = {}

        if len(asin) != 10 and len(asin) != 0:
            errors['invalid_asin'] = f"Invalid ASIN format for {asin}"

        if len(asin) == 0:
            errors['blank_asin'] = "Must fill in all ASIN fields"

        return errors


class SettingManager(models.Manager):
    def file_path_validator(self, path):
        errors = {}

        if not Path(path).is_dir():
            errors['invalid_path'] = f"Path is not a directory: {path}"
        return errors


class Book(models.Model):
    title = models.CharField(max_length=255)
    asin = models.CharField(max_length=10)
    short_desc = models.TextField()
    long_desc = models.TextField()
    release_date = models.DateField()
    series = models.CharField(max_length=255, blank=True, default='')
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


class Setting(models.Model):
    api_url = models.CharField(max_length=255)
    completed_directory = models.CharField(max_length=255)
    input_directory = models.CharField(max_length=255)
    num_cpus = models.IntegerField()
    output_directory = models.CharField(max_length=255)
    output_scheme = models.CharField(max_length=255)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)
    objects = SettingManager()
