from django.urls import path
from . import views
urlpatterns = [
	path('', views.importer),
	path('dir_selection', views.dir_selection),
	path('match', views.match),
	path('get_asin', views.get_asin),
	path('<str:asin>/confirm', views.finish)
]