# AGENTS.md - AI Assistant Guide for Reddit Media Downloader

This document provides guidance for AI agents working on the reddit-media-downloader project. It supplements the existing README.md with development-specific patterns, workflows, and best practices.

## Project Overview

**reddit-media-downloader** is a lightweight, efficient Reddit media downloader written in Go. It fetches upvoted and saved posts, downloads images and videos (including from external sites), and tracks downloads to avoid duplicates. Runs on a 1-hour Docker schedule.

### Core Purpose

- OAuth2 authentication with Reddit
- Concurrent media downloads (10 parallel by default)
- SQLite database for deduplication tracking
- Automatic migration from existing bdfr-html data
- Minimal Docker image (~15MB)

### Technology Stack

- **Language**: Go 1.23
- **Database**: SQLite (via `github.com/mattn/go-sqlite3`)
- **Key Dependencies**:
  - `golang.org/x/oauth2` - OAuth2 authentication
  - `golang.org/x/sync` - Semaphore for concurrency control
  - `github.com/joho/godotenv` - Environment variable loading

## Architecture

### Directory Structure

```
reddit-media-downloader/
├── cmd/
│   ├── downloader/
│   │   └── main.go              # Main downloader entry point
│   └── migrate/
│       └── main.go              # File reorganization tool
├── internal/
│   ├── config/                  # Configuration
│   │   └── config.go            # Environment variable loading
│   ├── reddit/                  # Reddit API client
│   │   ├── client.go            # OAuth2 client
│   │   ├── client_test.go       # Client tests
│   │   └── post.go              # Post types and structures
│   ├── downloader/              # Media download logic
│   │   ├── downloader.go        # Main downloader orchestration
│   │   ├── downloader_test.go   # Downloader tests
│   │   └── extractor.go         # URL extraction from posts
│   ├── storage/                 # SQLite database
│   │   ├── db.go                # Database operations
│   │   ├── db_test.go           # Database tests
│   │   └── post.go              # Stored post type
│   └── migration/               # File reorganization library
│       ├── extractor.go         # POSTID extraction
│       ├── parser.go            # HTML parsing
│       ├── migrator.go          # Migration logic
│       ├── rollback.go          # Rollback functionality
│       ├── types.go             # Migration types
│       ├── utils.go             # Utility functions
│       └── migration_test.go    # Migration tests
├── Dockerfile                   # Multi-stage build
├── docker-compose.yml           # Docker Compose config
├── .env.example                 # Environment template
└── README.md                    # Project overview
```

### Key Patterns

#### Configuration Pattern

Configuration is loaded from environment variables via `godotenv`:

```go
// Located in internal/config/config.go
type Config struct {
    ClientID     string
    ClientSecret string
    Username     string
    Password     string
    OutputDir    string
    DBPath       string
    Concurrency  int
    FetchLimit   int
    LogLevel     string
}

func LoadConfig() (*Config, error) {
    godotenv.Load()
    // ... load from env vars
}
```

#### OAuth2 Pattern

Reddit OAuth2 authentication follows this pattern (see `internal/reddit/client.go`):

1. Load OAuth2 configuration with device flow or password flow
2. Exchange credentials for access token
3. Use token to authenticate API requests
4. Handle token refresh if needed

#### Concurrency Pattern

Downloads use semaphores for concurrency control (see `internal/downloader/downloader.go`):

```go
sem := semaphore.NewWeighted(int64(cfg.Concurrency))
// Acquire before download, release after
```

## Development Workflow

### Building the Project

```bash
# Build main downloader
go build -o reddit-downloader cmd/downloader/main.go

# Build migration tool
go build -o migrate cmd/migrate/main.go

# Build all
go build ./...
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests in specific package
go test ./internal/downloader -v

# Run tests with race detection
go test -race ./...
```

### Running the Application

```bash
# With Docker Compose (recommended)
docker-compose up -d

# With binary
./reddit-downloader

# With migration tool
./migrate --source /path/to/source --dest /path/to/dest --index /path/to/index.html --dry-run
```

## Common Tasks

### Adding a New Reddit Post Type

1. Add post type to `internal/reddit/post.go`
2. Update URL extraction logic in `internal/downloader/extractor.go`
3. Add test cases in `internal/downloader/downloader_test.go`

### Adding Support for New Media Host

1. Update `internal/downloader/extractor.go` to recognize new host
2. Add download logic for new host in `internal/downloader/downloader.go`
3. Add tests for new host in test files

### Modifying Database Schema

1. Update `internal/storage/post.go` with new fields
2. Update SQL queries in `internal/storage/db.go`
3. Update migration logic if needed
4. Add tests in `internal/storage/db_test.go`

### Adding New Configuration Options

1. Add field to `Config` struct in `internal/config/config.go`
2. Add environment variable loading in `LoadConfig()`
3. Update `.env.example`
4. Update README.md documentation

## Configuration Files

### Environment Variables (.env)

Required variables:
- `REDDIT_CLIENT_ID` - Reddit API client ID
- `REDDIT_CLIENT_SECRET` - Reddit API client secret
- `REDDIT_USERNAME` - Reddit username

Optional variables:
- `REDDIT_PASSWORD` - Reddit password (for OAuth2 password flow)
- `REDDIT_USER_AGENT` - User agent string
- `OUTPUT_DIR` - Output directory path
- `DB_PATH` - SQLite database path
- `CONCURRENCY` - Number of parallel downloads
- `FETCH_LIMIT` - Posts per fetch
- `LOG_LEVEL` - Logging level
- `MIGRATE_ON_START` - Auto-migrate from bdfr-html

### Docker Compose

```yaml
services:
  reddit-downloader:
    build: .
    environment:
      - REDDIT_CLIENT_ID=${REDDIT_CLIENT_ID}
      - REDDIT_CLIENT_SECRET=${REDDIT_CLIENT_SECRET}
      - REDDIT_USERNAME=${REDDIT_USERNAME}
    volumes:
      - ./data:/app/data
```

## Testing Guidelines

### Test File Structure

Tests follow the pattern `${filename}_test.go` alongside source files:

- `internal/downloader/downloader_test.go` tests `downloader.go`
- `internal/reddit/client_test.go` tests `client.go`
- `internal/storage/db_test.go` tests `db.go`
- `internal/migration/migration_test.go` tests migration logic

### Test Patterns

```go
func TestDownloadMedia(t *testing.T) {
    tests := []struct {
        name     string
        setup    func()
        teardown func()
        // ...
    }{
        { /* test case */ },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tt.setup()
            defer tt.teardown()
            // test logic
        })
    }
}
```

### Mock Patterns

For testing Reddit API calls, use table-driven tests with mock responses.

## Best Practices

### Code Style

- Follow Go best practices and standard formatting (`gofmt`)
- Use named return values only when beneficial
- Handle errors explicitly; don't ignore errors
- Use `context.Context` for cancelable operations

### Error Handling

- Always check and handle errors
- Wrap errors with context using `fmt.Errorf`
- Return errors from functions; don't panic in library code
- Use structured logging with different log levels

### Concurrency

- Use semaphores for limiting concurrent operations
- Always release semaphore resources in defer
- Use goroutines and channels for concurrent processing
- Avoid data races; use `-race` flag during testing

### Security

- Never commit credentials or API keys
- Use environment variables for sensitive data
- Validate all user inputs
- Use parameterized SQL queries to prevent injection

### Performance

- Respect rate limits from Reddit API
- Use appropriate concurrency levels (default: 10)
- Reuse database connections
- Clean up resources in defer blocks

## Docker Deployment

### Building Docker Image

```bash
docker build -t reddit-media-downloader:latest .
```

### Build Stages

The Dockerfile uses multi-stage builds:
1. `builder` stage: Compiles Go code
2. `runner` stage: Uses minimal Alpine image

This results in a ~15MB final image.

### Running with Docker Compose

```bash
docker-compose up -d
docker-compose logs -f
docker-compose down
```

## Troubleshooting

### Build Fails

```bash
# Check Go version
go version

# Verify dependencies
go mod tidy

# Check Go modules
go list -m all
```

### Tests Fail

```bash
# Run with verbose output
go test -v ./...

# Run with race detection
go test -race ./...

# Check coverage
go test -cover ./...
```

### Docker Image Won't Build

```bash
# Clean Docker build cache
docker builder prune

# Build without cache
docker build --no-cache -t reddit-media-downloader .

# Check Dockerfile syntax
docker build --check .
```

### Authentication Fails

- Verify Reddit credentials in `.env`
- Check that Reddit app is configured as "script" type
- Ensure username/password are correct
- Check network connectivity to Reddit

### Downloads Fail

- Set `LOG_LEVEL=debug` for detailed logs
- Verify disk space in `OUTPUT_DIR`
- Check network connectivity to Reddit and external sites
- VerifyReddit API rate limits

## References

- [README.md](README.md) - Project overview and features
- GitHub: https://github.com/yourusername/reddit-media-downloader
- Reddit OAuth Documentation: https://www.reddit.com/prefs/apps
