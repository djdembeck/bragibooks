from django.urls import path
from . import views
urlpatterns = [
    path('', views.importer, name='home'),
    path('dir_selection', views.dir_selection),
    path('match', views.match, name='match'),
    path('get_asin', views.get_asin),
    path('confirm', views.finish, name='finish'),
    path('api_auth', views.api_auth),
    path('get_auth', views.get_auth)
]
