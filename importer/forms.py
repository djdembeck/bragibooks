from django import forms
from .models import Setting

class SettingForm(forms.ModelForm):
    class Meta:
        model = Setting
        fields = (
            'api_url',
            'completed_directory',
            'input_directory',
            'num_cpus',
            'output_directory',
            'output_scheme'
        )
        labels = {
            'api_url': 'Custom API URL',
            'completed_directory': 'Directory for copy of original input files',
            'input_directory': 'Input directory path',
            'num_cpus': 'Number of CPUs to use (0 will use all available)',
            'output_directory': 'Output directory path',
            'output_scheme': 'Output format ('/' denotes folder)'
        }
        widgets = {
            'api_url': forms.URLInput(attrs={'class': 'input is-fullwidth'}),
            'completed_directory': forms.TextInput(attrs={'class': 'input is-fullwidth'}),
            'input_directory': forms.TextInput(attrs={'class': 'input is-fullwidth'}),
            'num_cpus': forms.NumberInput(attrs={'class': 'input is-fullwidth'}),
            'output_directory': forms.TextInput(attrs={'class': 'input is-fullwidth'}),
            'output_scheme': forms.TextInput(attrs={'class': 'input is-fullwidth'}),
        }