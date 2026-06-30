#!/usr/bin/env bash

registry="ketidevit2"
image_name="cost-based-scheduler"
version="${1:-0.4.0}"
dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

echo "================================"
echo "Building Cost Based Scheduler"
echo "Registry: $registry"
echo "Image: $image_name:$version"
echo "================================"

# Create build directory if not exists
mkdir -p "$dir/../build/_output/bin"

# Build static binary for Linux
echo "Building Go binary..."
cd "$dir/.." || exit 1
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o "$dir/../build/_output/bin/cost-based-scheduler" \
    "$dir/../cmd/main.go"

if [ $? -ne 0 ]; then
    echo "Error: Go build failed"
    exit 1
fi

echo "Binary built successfully"

# Build Docker image
echo "Building Docker image..."
docker build -t "$image_name:$version" "$dir/../build"

if [ $? -ne 0 ]; then
    echo "Error: Docker build failed"
    exit 1
fi

echo "Docker image built successfully"

# Tag image
echo "Tagging image..."
docker tag "$image_name:$version" "$registry/$image_name:$version"

# Push image
echo "Pushing image to registry..."
docker push "$registry/$image_name:$version"

if [ $? -ne 0 ]; then
    echo "Error: Docker push failed"
    exit 1
fi

echo "================================"
echo "Build and push completed successfully!"
echo "Image: $registry/$image_name:$version"
echo "================================"
