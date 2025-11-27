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

# Run the application with environment variables
# WARNING: Multi-worker mode (UVICORN_WORKERS>1) requires Redis for shared cache
# Current in-memory cache does NOT sync across workers
# For production: Use UVICORN_WORKERS=1 OR implement Redis cache
CMD uvicorn app.main:app --host ${HOST:-0.0.0.0} --port ${PORT:-8000} --workers ${UVICORN_WORKERS:-1}
