from django.shortcuts import render, redirect
# from login.models import User
# from .models import Book
# from django.contrib import messages

def importer(request):
	return render(request, "import.html")

def match(request):
	return render(request, "match.html")