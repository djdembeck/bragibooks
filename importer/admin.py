from django.contrib import admin
from .models import Author, Book, Narrator, Status

admin.site.register(Book)
admin.site.register(Author)
admin.site.register(Narrator)
admin.site.register(Status)
