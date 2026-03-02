# Build and push Docker image to GitHub Container Registry
IMAGE ?= "ghcr.io/OWNER/matrix-tinder:latest"

docker-build:
    #!/usr/bin/env bash
    # Use multiarch builder if available
    if docker buildx ls | grep -q "^multiarch "; then
        docker buildx use multiarch
    fi
    
    docker buildx build \
        --platform linux/amd64,linux/arm64 \
        -t {{IMAGE}} \
        . --push
