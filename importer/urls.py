from django.urls import path
from . import views
urlpatterns = [
    path('', views.ImportView.as_view(), name='home'),
    path('match', views.MatchView.as_view(), name='match'),
    path('confirm', views.FinishView.as_view(), name='finish'),
    path('auth', views.AuthView.as_view(), name="auth"),
]
