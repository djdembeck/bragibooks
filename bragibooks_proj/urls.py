from django.conf import settings
from django.contrib import admin
from django.urls import path, include


urlpatterns = [
    path('', include('importer.urls')),
]
    
if settings.DEBUG:
    urlpatterns += path('admin/', admin.site.urls),
