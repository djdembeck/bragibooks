from django.urls import path
from . import views
urlpatterns = [
	path('', views.importer),
	path('dir_selection', views.dir_selection),
	path('match', views.match),
	path('get_asin', views.get_asin),
	path('confirm', views.finish),
	path('api_auth', views.api_auth),
	path('get_auth', views.get_auth)
]