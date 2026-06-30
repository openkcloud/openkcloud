# Stage 1: Build stage to install dependencies
FROM python:3.12-slim AS builder

WORKDIR /app

# Copy requirements file
COPY requirements.txt ./

# Install dependencies to a target directory
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

# Stage 2: Final stage
FROM python:3.12-slim

WORKDIR /app

# Runtime env: unbuffered logs, no .pyc files
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

# Create a non-root user
RUN addgroup --system app && adduser --system --group app

# Copy installed dependencies from the builder stage
COPY --from=builder /install /usr/local

# Copy application code
COPY ./app ./app

# Set ownership and switch to non-root user
RUN chown -R app:app /app
USER app

# Expose port (will be set by environment variable)
EXPOSE ${PORT:-8000}

# Liveness healthcheck via the public probe endpoint (no curl needed in slim image)
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD python -c "import os,urllib.request; urllib.request.urlopen('http://127.0.0.1:'+os.environ.get('PORT','8000')+'/api/v1/system/livez').read()" || exit 1

# Run the application with environment variables
# WARNING: Multi-worker mode (UVICORN_WORKERS>1) requires Redis for shared cache
# Current in-memory cache does NOT sync across workers
# For production: Use UVICORN_WORKERS=1 OR implement Redis cache
CMD uvicorn app.main:app --host ${HOST:-0.0.0.0} --port ${PORT:-8000} --workers ${UVICORN_WORKERS:-1}
