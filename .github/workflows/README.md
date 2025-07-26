# GitHub Actions Workflows

## Docker Publish Workflow

This workflow automatically builds and publishes the Docker image to GitHub Container Registry (ghcr.io) when:
- Code is pushed to the master branch
- A new tag is pushed (starting with 'v')
- A pull request is created against master (builds but doesn't push)

### How It Works

1. The workflow uses GitHub's container registry (ghcr.io)
2. Authentication is handled automatically using GITHUB_TOKEN
3. Images are tagged based on:
   - Branch name (for branch pushes)
   - PR number (for pull requests)
   - Semantic version (for version tags like v1.0.0)
   - Major.minor version (for version tags, like 1.0)
   - Git SHA (long format)
   - Latest (for default branch)

### Prerequisites

- No additional secrets needed as it uses the default GITHUB_TOKEN
- Permissions are set in the workflow: read contents, write packages

### Usage

To use this workflow:

1. Push to master branch:
   - Builds and publishes image as `ghcr.io/username/repo:latest`

2. Create and push a tag:
   ```bash
   git tag v1.0.0
   git push origin v1.0.0
   ```
   - Builds and publishes image as `ghcr.io/username/repo:1.0.0`, `ghcr.io/username/repo:1.0`, and with SHA tag

3. Create a pull request:
   - Builds the image (doesn't push) to verify it can be built successfully

### Pulling the Image

Once published, the image can be pulled with:

```bash
docker pull ghcr.io/your-username/your-repo:latest
```

Remember to replace `your-username/your-repo` with your actual GitHub username and repository name.