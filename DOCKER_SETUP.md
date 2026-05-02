# Docker Setup Guide

This guide explains how to set up automatic Docker image publishing to Docker Hub and GitHub Container Registry.

## Prerequisites

1. A GitHub account with this repository
2. A Docker Hub account (for Docker Hub publishing)

## Step 1: Configure GitHub Secrets

You need to add the following secrets to your GitHub repository:

### For Docker Hub Publishing

1. Go to your GitHub repository
2. Click on **Settings** > **Secrets and variables** > **Actions**
3. Click **New repository secret**
4. Add the following secrets:

   - **DOCKERHUB_USERNAME**
     - Name: `DOCKERHUB_USERNAME`
     - Value: Your Docker Hub username (e.g., `myusername`)

   - **DOCKERHUB_TOKEN**
     - Name: `DOCKERHUB_TOKEN`
     - Value: Your Docker Hub access token

### How to Get Docker Hub Access Token

1. Log in to [Docker Hub](https://hub.docker.com/)
2. Go to **Account Settings** > **Security**
3. Click **New Access Token**
4. Give it a description (e.g., "GitHub Actions")
5. Select permissions: **Read, Write, Delete** (recommended for CI/CD)
6. Copy the token immediately (you won't be able to see it again)

## Step 2: Update Repository Name

In `.github/workflows/docker-publish.yml`, the image name is automatically set from your repository name:

```yaml
env:
  IMAGE_NAME: ${{ github.repository }}
```

This will use `username/repo-name` format. If you want to customize it:

```yaml
env:
  IMAGE_NAME: yourusername/cloudflared-ui
```

## Step 3: Update docker-compose.yml

Update the image name in `docker-compose.yml`:

```yaml
services:
  cfui:
    image: ghcr.io/dockers-x/cfui:latest
    # Or use GitHub Container Registry:
    # image: yourusername/cloudflared-ui:latest
```

## Step 4: Create a Release

The workflow will trigger on:

1. **Push to main/master branch**: Creates `latest` tag
2. **Create a tag**: Create version tags (e.g., `v1.0.0`)
3. **Manual trigger**: Via GitHub Actions UI

### Creating a Version Release

```bash
# Tag the release
git tag v1.0.0
git push origin v1.0.0
```

This will create the following Docker tags:
- `latest` (if on default branch)
- `1.0.0`
- `1.0`
- `1`

## Step 5: Verify Published Images

### Docker Hub

1. Go to https://hub.docker.com/r/yourusername/cloudflared-ui
2. Check the **Tags** section

### GitHub Container Registry

1. Go to your GitHub repository
2. Click on **Packages** (right sidebar)
3. Find `cloudflared-ui` package

## Using the Published Images

### From Docker Hub

```bash
docker pull yourusername/cloudflared-ui:latest
docker run -d -p 14333:14333 -v cloudflared-data:/app/data yourusername/cloudflared-ui:latest
```

### From GitHub Container Registry

```bash
# Login (if repository is private)
echo $GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin

# Pull and run
docker pull ghcr.io/yourusername/cloudflared-ui:latest
docker run -d -p 14333:14333 -v cloudflared-data:/app/data ghcr.io/yourusername/cloudflared-ui:latest
```

### Optional Remote Tunnel Manager Environment

The image can also manage Cloudflare-hosted tunnel public hostname rules. This
feature is disabled by default and is independent from the local cloudflared
runner.

Prefer secrets or environment injection instead of committing credentials:

```yaml
environment:
  - CFUI_TUNNEL_MGMT_ENABLED=true
  - CLOUDFLARE_ACCOUNT_ID=your-account-id
  - CLOUDFLARE_TUNNEL_ID=your-tunnel-id
  - CLOUDFLARE_API_TOKEN=your-api-token
```

Alternative global API key auth is also supported:

```yaml
environment:
  - CFUI_TUNNEL_MGMT_ENABLED=true
  - CLOUDFLARE_ACCOUNT_ID=your-account-id
  - CLOUDFLARE_TUNNEL_ID=your-tunnel-id
  - CLOUDFLARE_API_EMAIL=you@example.com
  - CLOUDFLARE_API_KEY=your-global-api-key
```

### Using Docker Compose

```bash
docker-compose up -d
```

## Supported Platforms

The workflow builds multi-architecture images supporting:

- `linux/amd64` (x86_64)
- `linux/arm64` (ARM 64-bit, e.g., Raspberry Pi 4/5, Apple Silicon)
- `linux/arm/v7` (ARM 32-bit, e.g., Raspberry Pi 3)

## Troubleshooting

### Build Fails

1. Check the GitHub Actions logs
2. Verify all secrets are correctly set
3. Ensure Dockerfile syntax is correct

### Can't Pull Image

1. Check if the image name is correct
2. For private repositories, ensure you're logged in
3. Verify the tag exists

### Permission Denied

1. Verify Docker Hub token has correct permissions
2. For GHCR, ensure GitHub Actions has package write permissions

## Security Best Practices

1. **Never commit tokens or secrets** to your repository
2. **Use access tokens**, not passwords
3. **Rotate tokens regularly**
4. **Use least privilege**: Only grant necessary permissions
5. **Enable 2FA** on Docker Hub and GitHub

## Additional Resources

- [GitHub Actions Documentation](https://docs.github.com/en/actions)
- [Docker Hub Documentation](https://docs.docker.com/docker-hub/)
- [GitHub Container Registry Documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
