from django.urls import path, re_path
from . import views

urlpatterns = [
    path("", views.ImportView.as_view(), name="import"),
    path("match", views.MatchView.as_view(), name="match"),
    re_path(r"^asin-search/$", views.AsinSearch.as_view(), name="asin-search"),
    path("books", views.BookListView.as_view(), name="books"),
    path("setting", views.SettingView.as_view(), name="setting"),
    path("api/directories/", views.DirectoryListView.as_view(), name="api-directories"),
    path(
        "api/directories/stream/",
        views.StreamDirectoryListView.as_view(),
        name="api-directories-stream",
    ),
]
