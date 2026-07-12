from django.conf import settings
from django.contrib import admin
from django.urls import include, path

urlpatterns = [
    path("", include("importer.urls")),
]

if settings.DEBUG:
    urlpatterns += (path("admin/", admin.site.urls),)
