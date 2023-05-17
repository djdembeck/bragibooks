from django.db import migrations, models
from m4b_merge import audible_helper, config

from utils.merge import set_configs

def add_cover_image_link(apps, _):
    Book = apps.get_model("importer", "Book")
    set_configs()

    for book in Book.objects.all():
        metadata = audible_helper.BookData(book.asin).fetch_api_data(config.api_url)
        book.cover_image_link = metadata['image']
        book.save()

class Migration(migrations.Migration):

    dependencies = [
        ('importer', '0004_book_status'),
    ]

    operations = [
        migrations.AddField(
            model_name='book',
            name='cover_image_link',
            field=models.URLField(blank=True, null=True),
            preserve_default=False,
        ),

        migrations.RunPython(add_cover_image_link, migrations.RunPython.noop),

        migrations.AlterField(
            model_name='book',
            name='cover_image_link',
            field=models.URLField(null=False)
        ),
    ]
