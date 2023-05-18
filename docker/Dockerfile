# Base image
FROM ghcr.io/djdembeck/m4b-merge:develop

# Keeps Python from generating .pyc files in the container
ENV PYTHONDONTWRITEBYTECODE=1

# Turns off buffering for easier container logging
ENV PYTHONUNBUFFERED=1

# Setup environment variable
ENV APP_HOME=/home/app/web

# Where your code lives  
WORKDIR $APP_HOME

# Copy the necessary files to the container
COPY requirements.txt .

# Run this command to install all dependencies
RUN pip install --no-cache-dir --upgrade pip && \
    pip install --no-cache-dir -r requirements.txt

# Copy whole project to your docker home directory.
COPY bragibooks_proj $APP_HOME/bragibooks_proj/
COPY importer $APP_HOME/importer/
COPY utils $APP_HOME/utils/
COPY manage.py $APP_HOME

# Copy entrypoint file
COPY docker/entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/entrypoint.sh

# Port where the Django app runs
EXPOSE 8000

# Entrypoint for the container
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
