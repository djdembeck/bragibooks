from django.urls import path
from . import views
urlpatterns = [
    path('', views.ImportView.as_view(), name='home'),
    path('match', views.MatchView.as_view(), name='match'),
    path('setting', views.SettingView.as_view(), name='setting'),
    path('confirm', views.FinishView.as_view(), name='finish'),
]
