from django.urls import path
from . import views
urlpatterns = [
	path('', views.importer),
	path('match', views.match),
]