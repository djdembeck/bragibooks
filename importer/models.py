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
            try:
                Path(path).mkdir(parents=True, exist_ok=True)
            except OSError:
                errors['invalid_path'] = (
                    f"Invalid path: {path}"
                )
        return errors


class StatusChoices(models.TextChoices):
    PROCESSING = "Processing"
    DONE = "Done"
    ERROR = "Error"

class Status(models.Model):
    status = models.CharField(max_length=10, choices=StatusChoices.choices)
    message = models.TextField()

    def __str__(self) -> str:
        return self.status

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
    src_path = models.TextField()
    dest_path = models.TextField()
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)
    status = models.OneToOneField(Status, on_delete=models.CASCADE)
    objects = BookManager()

    def __str__(self) -> str:
        return f"{self.title}: by {', '.join(str(author) for author in self.authors.all())}"

class Author(models.Model):
    first_name = models.CharField(max_length=45)
    last_name = models.CharField(max_length=45)
    asin = models.CharField(max_length=10, null=True, default='')
    books = models.ManyToManyField(Book, related_name="authors")
    short_desc = models.TextField(blank=True, default='')
    long_desc = models.TextField(blank=True, default='')
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    def __str__(self) -> str:
        return f"{self.first_name} {self.last_name}"

class Narrator(models.Model):
    first_name = models.CharField(max_length=45)
    last_name = models.CharField(max_length=45)
    books = models.ManyToManyField(Book, related_name="narrators")
    short_desc = models.TextField(blank=True, default='')
    long_desc = models.TextField(blank=True, default='')
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    def __str__(self) -> str:
        return f"{self.first_name} {self.last_name}"

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
