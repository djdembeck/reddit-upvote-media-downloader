# Contributing

Thank you for your interest in contributing to reddit-upvote-media-downloader!

## Development Workflow

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Commit using [Conventional Commits](https://www.conventionalcommits.org/) (see below)
5. Push to your fork
6. Create a pull request

## Conventional Commits

This project uses **conventional commits** to automatically determine version bumps and generate changelogs.

### Format

```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

### Commit Types

| Type | Bump Type | Usage |
|------|-----------|-------|
| `feat` | **minor** | New feature or functionality |
| `fix` | **patch** | Bug fix |
| `perf` | **patch** | Performance improvement |
| `chore` | **patch** | Maintenance tasks (deps, build, etc.) |
| `docs` | **patch** | Documentation only changes |
| `style` | **patch** | Code style changes (formatting, etc.) |
| `refactor` | **patch** | Code refactoring without functional changes |
| `test` | **patch** | Adding or updating tests |
| `ci` | **patch** | CI/CD configuration changes |
| `revert` | **patch** | Revert previous commit |

### Breaking Changes

To trigger a **major** version bump:

1. Add `BREAKING CHANGE:` or `breaking!` to your commit:

   ```
   feat!: remove deprecated API

   BREAKING CHANGE: This removes the old API endpoint
   ```

   or

   ```
   feat(api)!: change return type from string to int

   BREAKING CHANGE: This changes the public API signature
   ```

2. Or add `!` after the type/scope:

   ```
   feat!: completely change the authentication flow
   ```

### Examples

#### Adding a feature (minor bump)
```bash
git commit -m "feat: add support for downloading from external video host"
```

#### Bug fix (patch bump)
```bash
git commit -m "fix: resolve OAuth token refresh failure"
```

#### Performance improvement (patch bump)
```bash
git commit -m "perf: reduce memory usage during concurrent downloads"
```

#### Breaking change (major bump)
```bash
git commit -m "feat(api)!: change environment variable naming convention

BREAKING CHANGE: All environment variables now use underscores consistently"
```

#### Documentation (patch bump)
```bash
git commit -m "docs: update README with new configuration examples"
```

## Release Process

Releases are **automated** and triggered on push to the `main` branch:

1. Analyze commits since last release
2. Determine bump type (major/minor/patch) based on commit types
3. Calculate new version number
4. Create git tag
5. Push tag to GitHub
6. Create GitHub Release with changelog
7. Trigger Docker Build workflow (tags push image)

### Example Release Flow

```bash
# Developer makes a feature commit
git commit -m "feat: add image deduplication"
git push origin feature/new-feature

# PR merges to main

# Release workflow automatically:
# - Detects "feat" commit → minor bump
# - Creates tag (e.g., v1.2.0 → v1.3.0)
# - Creates GitHub Release
# - Builds and pushes Docker images
```

### Manual Release

To create a manual release with a specific version:

```bash
git tag -a v1.2.3 -m "Release v1.2.3"
git push origin v1.2.3
```

This will:
- Trigger the Docker Build workflow
- Create a GitHub Release (add release notes manually)

## Development Setup

### Prerequisites

- Go 1.23 or later
- Docker (optional, for containerized development)
- Make (optional, for running make commands)

### Build

```bash
go build -o reddit-downloader cmd/downloader/main.go
```

### Run Tests

```bash
go test ./...
go test -race ./...
go test -cover ./...
```

### Development with Docker

```bash
# Build and run
docker-compose up --build

# View logs
docker-compose logs -f

# Stop
docker-compose down
```

### Linting

```bash
gofmt -s -w .
go vet ./...
```

## Code Style

- Follow standard Go conventions (`gofmt`)
- Use meaningful variable names
- Handle errors explicitly
- Write tests for new features
- Keep functions small and focused

## Pull Request Guidelines

1. **Title**: Use conventional commit format
   - `feat: add external video host support` ✅
   - `Fix authentication bug` ❌

2. **Description**: Explain the why, not just the what
   - What problem does it solve?
   - Why this approach?
   - Any trade-offs?

3. **Tests**: Include tests for new functionality
4. **Docs**: Update documentation as needed
5. **Clean**: Remove debug code, commented sections, etc.

## Troubleshooting

### Release not triggered

1. Check workflow run logs in `.github/workflows/release.yml`
2. Verify commit is pushed to `main` branch
3. Ensure commit is not already tagged

### Version bump incorrect

1. Review commit messages since last tag
2. Check for `BREAKING CHANGE:` or `breaking!` in commits
3. Verify commit type prefix (feat, fix, perf, etc.)

### Docker image not building

1. Check `.github/workflows/docker-build.yml` logs
2. Verify tag was pushed successfully
3. Check Container Registry permissions

## Questions?

Open an issue or pull request with your question!
