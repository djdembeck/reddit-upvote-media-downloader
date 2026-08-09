# Reddit Upvote Media Downloader

A lightweight Go downloader that archives media from your upvoted and saved Reddit posts.

## Long Description

Reddit Upvote Media Downloader periodically fetches images and videos from a user's upvoted and saved posts, including media hosted by Reddit, Imgur, Gfycat, and Redgifs. It stores media files locally and uses SQLite-backed tracking and BLAKE3 hashes to avoid downloading the same media again. It is intended for private archives on a home server, NAS, or VPS and is suitable for hourly Docker or cron-style execution.

This project replaces bdfr-html with a focused downloader: it saves media files without generating HTML reports or JSON sidecars. Its multi-stage Docker image is approximately 15 MB instead of bdfr-html's approximately 900 MB, and the Go process typically uses 10–20 MB of memory with sub-100 ms startup time.

## Table of Contents

- [Long Description](#long-description)
- [Table of Contents](#table-of-contents)
- [Background](#background)
  - [Features](#features)
  - [Comparison with bdfr-html](#comparison-with-bdfr-html)
- [Install/Running](#installrunning)
  - [Docker with the prebuilt image](#docker-with-the-prebuilt-image)
  - [Docker Compose](#docker-compose)
  - [Binary](#binary)
- [Usage](#usage)
  - [Quickstart](#quickstart)
  - [Configuration](#configuration)
    - [Required variables](#required-variables)
    - [Optional variables](#optional-variables)
    - [Example `.env` file](#example-env-file)
    - [Docker Compose environment](#docker-compose-environment)
  - [CLI Flags](#cli-flags)
  - [Re-check Mode (`--re-check`)](#re-check-mode---re-check)
  - [Retry and Exponential Backoff](#retry-and-exponential-backoff)
  - [Migration from bdfr-html](#migration-from-bdfr-html)
    - [Automatic file reorganization](#automatic-file-reorganization)
    - [Full sync behavior](#full-sync-behavior)
    - [Re-check after migration](#re-check-after-migration)
  - [File Reorganization Tool](#file-reorganization-tool)
  - [Reddit OAuth Setup](#reddit-oauth-setup)
  - [Project Structure](#project-structure)
  - [Troubleshooting](#troubleshooting)
    - [Docker image will not build](#docker-image-will-not-build)
    - [Authentication fails](#authentication-fails)
    - [Downloads fail](#downloads-fail)
    - [Migration issues](#migration-issues)
- [Building](#building)
  - [Prerequisites](#prerequisites)
  - [Build the binaries](#build-the-binaries)
  - [Build the Docker image](#build-the-docker-image)
- [Contributing](#contributing)
- [Acknowledgements](#acknowledgements)
- [License](#license)

## Background

The project is for self-hosters and personal-backup users who want a searchable local copy of media they have interacted with on Reddit. Existing bulk downloaders can generate substantial HTML and JSON metadata; this project keeps the archive focused on media files and provides a migration path for existing bdfr-html collections.

### Features

- ✅ OAuth2 authentication with Reddit
- ✅ Fetches both **upvoted** and **saved** posts
- ✅ Downloads from Reddit-hosted and external sources (Imgur, Gfycat, Redgifs)
- ✅ Concurrent downloads (10 parallel by default)
- ✅ SQLite database for deduplication tracking
- ✅ Hash tracking so re-runs do not re-download the same media
- ✅ Automatic migration from existing bdfr-html data
- ✅ Optional re-check mode to re-download files missing from disk
- ✅ Exponential backoff retry for rate limiting and transient failures
- ✅ Minimal Docker image (~15MB)
- ✅ Hourly scheduled execution
- ✅ Graceful shutdown handling and structured logging

### Comparison with bdfr-html

| Feature | bdfr-html | This Project |
|---------|-----------|--------------|
| Docker Image | ~900MB | ~15MB (60x smaller) |
| Memory Usage | 100-200MB | 10-20MB |
| Startup Time | 2-5 seconds | <100ms |
| Concurrency | Limited | 10+ parallel downloads |
| HTML Generation | Yes | **No** (not needed) |
| JSON Metadata | Yes | **No** (not needed) |

## Install/Running

### Docker with the prebuilt image

The prebuilt image is the fastest way to run the downloader without a local Go toolchain. The workflow publishes the `main` branch image to GHCR.

Create `.env` from the repository template, edit the Reddit credentials, and mount a local `data` directory. The explicit `OUTPUT_DIR` and `DB_PATH` values point the container at the mounted volume instead of the binary's relative-path defaults:

```bash
cp .env.example .env
# Edit .env with your Reddit credentials.
mkdir -p data

docker run -d \
  --name reddit-upvote-media-downloader \
  --env-file .env \
  -e OUTPUT_DIR=/data/output \
  -e DB_PATH=/data/posts.db \
  -v "$PWD/data:/data" \
  ghcr.io/djdembeck/reddit-upvote-media-downloader:main
```

View logs or stop the container with:

```bash
docker logs -f reddit-upvote-media-downloader
docker stop reddit-upvote-media-downloader
```

### Docker Compose

The repository's Compose file builds the image locally and runs it with `restart: unless-stopped`:

```bash
cd /path/to/reddit-upvote-media-downloader
cp .env.example .env
# Edit .env with your Reddit credentials.
docker compose up -d
```

The legacy `docker-compose` command is equivalent where that executable is installed:

```bash
docker-compose up -d
```

The Compose service mounts `./data` at `/data`, loads `.env` read-only into the container, and defaults to 10 concurrent downloads, a fetch limit of 100, a 200 ms download delay, `info` logging, and automatic migration. Follow logs with `docker compose logs -f reddit-upvote-media-downloader`.

### Binary

If you already have a built `reddit-downloader` binary, configure the environment as described below and run:

```bash
./reddit-downloader
```

Build commands for the downloader and migration binaries are in [Building](#building).

## Usage

### Quickstart

These are the commands most users run first:

```bash
# Start the configured Compose service.
docker compose up -d

# Run the OAuth2 setup flow when using a local binary.
./reddit-downloader --auth

# Re-check missing files with a larger download limit and debug logging.
LOG_LEVEL=debug CONCURRENCY=20 ./reddit-downloader --re-check --fetch-limit 100
```

Use `./reddit-downloader --help` for the complete downloader flag list. The downloader reads `.env` automatically and also accepts system or Docker environment variables. Configuration priority is CLI flags, then environment variables, then `.env`, then defaults.

### Configuration

Create `.env` from `.env.example`. The OAuth walkthrough below explains how to obtain the Reddit application values.

<details>
<summary>Full environment configuration</summary>

The application reads environment variables from a `.env` file loaded automatically, Docker environment variables, or system environment variables.

#### Required variables

| Variable | Description | Example |
|----------|-------------|---------|
| `REDDIT_CLIENT_ID` | Reddit API client ID | `U-6gk4ZCh3IeNQ` |
| `REDDIT_CLIENT_SECRET` | Reddit API client secret | `7CZHY6AmKweZME5s50SfDGylaPg` |
| `REDDIT_USERNAME` | Your Reddit username | `myusername` |
| `REDDIT_PASSWORD` or `REDDIT_REFRESH_TOKEN` | Reddit password or an OAuth refresh token | `your_reddit_password` |

#### Optional variables

| Variable | Default | Description |
|----------|---------|-------------|
| `REDDIT_USER_AGENT` | `reddit-upvote-media-downloader/1.0` | Reddit API user agent string |
| `REDDIT_PASSWORD` | *(empty)* | Reddit password; use a refresh token instead when appropriate |
| `REDDIT_REFRESH_TOKEN` | *(empty)* | OAuth refresh token generated by `--auth` |
| `OUTPUT_DIR` | `./data/output` | Directory to save downloaded media |
| `DB_PATH` | `./data/posts.db` | SQLite database file path |
| `CONCURRENCY` | `10` | Number of parallel downloads |
| `FETCH_LIMIT` | `100` | Number of posts to fetch per cycle |
| `DOWNLOAD_DELAY_MS` | `200ms` | Delay between downloads to avoid rate limiting |
| `MAX_RETRIES` | `3` | Maximum download attempts used by the downloader |
| `RETRY_THRESHOLD` | `3` | Max retries before permanently skipping a failed post |
| `BACKOFF_BASE` | `5s` | Base delay for exponential backoff |
| `BACKOFF_MAX` | `60s` | Maximum backoff delay |
| `RE_CHECK` | `false` | Enable re-check mode to verify and re-download missing files |
| `FULL_SYNC_ONCE` | `true` | First run after migration fetches all posts |
| `LOG_LEVEL` | `info` | Logging level: `debug`, `info`, `warn`, `error` |
| `MIGRATE_ON_START` | `true` | Auto-import existing bdfr-html data on first run |
| `MIGRATE_REORGANIZE` | `false` | Reorganize files into subreddit folders during migration |
| `MIGRATE_SOURCE_DIR` | *(empty)* | Source directory containing files to reorganize |
| `MIGRATE_HTML_DIR` | *(empty)* | Directory with bdfr-html files for metadata (optional) |
| `PUID` | `0` locally; `1000` in the Docker image | UID for files created by the container |
| `PGID` | `0` locally; `1000` in the Docker image | GID for files created by the container |

#### Example `.env` file

```env
# Reddit API Credentials (required)
# Get these from https://www.reddit.com/prefs/apps
REDDIT_CLIENT_ID=your_client_id_here
REDDIT_CLIENT_SECRET=your_client_secret_here
REDDIT_USER_AGENT="script:reddit-media-downloader:v1.0 (by /u/your_username)"
REDDIT_USERNAME=your_reddit_username
REDDIT_PASSWORD=your_reddit_password
# Or use the refresh token returned by --auth.
# REDDIT_REFRESH_TOKEN=your_refresh_token_here

# Download Settings (optional)
OUTPUT_DIR=./data/output
DB_PATH=./data/posts.db
CONCURRENCY=10
FETCH_LIMIT=100
DOWNLOAD_DELAY_MS=200ms

# Retry and Backoff (optional)
MAX_RETRIES=3
RETRY_THRESHOLD=3
BACKOFF_BASE=5s
BACKOFF_MAX=60s

# Re-check Mode (optional)
RE_CHECK=false

# Full Sync (optional)
FULL_SYNC_ONCE=true

# Logging (optional)
LOG_LEVEL=info

# Migration (optional)
MIGRATE_ON_START=true
MIGRATE_REORGANIZE=false
# MIGRATE_SOURCE_DIR=/path/to/bdfr-html/output
# MIGRATE_HTML_DIR=/path/to/bdfr-html/output

# File Ownership (Docker)
PUID=1000
PGID=1000
```

#### Docker Compose environment

The Compose file accepts the following environment variables and supplies container paths for storage:

```yaml
services:
  reddit-upvote-media-downloader:
    environment:
      - REDDIT_CLIENT_ID=${REDDIT_CLIENT_ID}
      - REDDIT_CLIENT_SECRET=${REDDIT_CLIENT_SECRET}
      - REDDIT_USER_AGENT=${REDDIT_USER_AGENT}
      - REDDIT_USERNAME=${REDDIT_USERNAME}
      - REDDIT_PASSWORD=${REDDIT_PASSWORD}
      - REDDIT_REFRESH_TOKEN=${REDDIT_REFRESH_TOKEN}
      - OUTPUT_DIR=/data/output
      - DB_PATH=/data/posts.db
      - CONCURRENCY=${CONCURRENCY:-10}
      - FETCH_LIMIT=${FETCH_LIMIT:-100}
      - DOWNLOAD_DELAY_MS=${DOWNLOAD_DELAY_MS:-200ms}
      - LOG_LEVEL=${LOG_LEVEL:-info}
      - MIGRATE_ON_START=${MIGRATE_ON_START:-true}
      - MIGRATE_REORGANIZE=${MIGRATE_REORGANIZE:-false}
      - MIGRATE_SOURCE_DIR=${MIGRATE_SOURCE_DIR:-}
      - MIGRATE_HTML_DIR=${MIGRATE_HTML_DIR:-}
      - PUID=${PUID:-1000}
      - PGID=${PGID:-1000}
```

</details>

### CLI Flags

The downloader supports the following command-line flags. Environment variables remain available for settings not supplied on the command line.

```bash
./reddit-downloader --re-check --retry-threshold 5 --concurrency 20
```

| Flag | Default | Description |
|------|---------|-------------|
| `--auth` | `false` | Run OAuth2 authentication to get a refresh token |
| `--re-check` | `false` | Enable re-check mode to verify files exist on disk and re-download missing ones |
| `--retry-threshold` | `3` | Maximum retries before permanently skipping a failed post |
| `--client-id` | *(from env)* | Reddit API client ID |
| `--client-secret` | *(from env)* | Reddit API client secret |
| `--username` | *(from env)* | Reddit username |
| `--concurrency` | `10` | Number of parallel downloads |
| `--fetch-limit` | `100` | Posts per fetch |
| `--backoff-base` | `5s` | Base backoff delay for retries |
| `--backoff-max` | `60s` | Max backoff delay for retries |
| `--help` | - | Show help message |
| `--version` | - | Show version information |

### Re-check Mode (`--re-check`)

When enabled, the downloader will:

1. Scan the output directory for existing files.
2. Compare the files against the SQLite database.
3. Re-download files that are recorded in the database but missing from disk.

This is useful for recovering from partial downloads, disk corruption, or accidental file deletion.

```bash
./reddit-downloader --re-check
```

### Retry and Exponential Backoff

When a download fails, the application uses exponential backoff before retrying:

1. **First failure**: Wait `BACKOFF_BASE` (default: 5s)
2. **Second failure**: Wait `2 * BACKOFF_BASE` (default: 10s)
3. **Third failure**: Wait `4 * BACKOFF_BASE` (default: 20s)
4. **Fourth failure**: Wait `8 * BACKOFF_BASE` (default: 40s)
5. **Fifth failure**: Wait `BACKOFF_MAX` (default: 60s)

After `RETRY_THRESHOLD` failures (default: 3), the post is permanently skipped and marked as failed in the database.

```bash
./reddit-downloader --backoff-base=10s --backoff-max=120s --retry-threshold=5
```

### Migration from bdfr-html

The downloader automatically migrates existing bdfr-html data on first run when `MIGRATE_ON_START=true`:

1. **Import from `idList.txt`**: Existing post IDs are imported into SQLite.
2. **Scan media files**: Existing media files are discovered and tracked.
3. **No re-downloads**: Already downloaded posts are skipped.

To migrate, copy your existing bdfr-html output into the new data directory:

```bash
cp -r /path/to/bdfr-html/output/* ./data/output/
cp /path/to/bdfr-html/output/idList.txt ./data/
```

Start the downloader with `MIGRATE_ON_START=true`. Logs will show: *"Migrated X existing posts from bdfr-html"*.

<details>
<summary>Migration and synchronization details</summary>

#### Automatic file reorganization

If your media files are in a flat structure, you can reorganize them into subreddit-based folders during migration:

```bash
MIGRATE_REORGANIZE=true
MIGRATE_SOURCE_DIR=/path/to/flat/media/directory
MIGRATE_HTML_DIR=/path/to/bdfr-html/output  # Optional: for metadata extraction
```

The migration:

1. Moves files from `MIGRATE_SOURCE_DIR` to `OUTPUT_DIR`, organized by subreddit.
2. Extracts HTML metadata to map post IDs to subreddits.
3. Populates the database with reorganized file paths.
4. Saves a migration log for rollback if needed.

Example Docker setup:

```yaml
environment:
  - MIGRATE_ON_START=true
  - MIGRATE_REORGANIZE=true
  - MIGRATE_SOURCE_DIR=/data/old_media
volumes:
  - ./data:/data
  - /path/to/old/bdfr-html/output:/data/old_media:ro
```

#### Full sync behavior

When `FULL_SYNC_ONCE=true` (default), the first run after migration fetches **all** upvoted and saved posts from Reddit. Subsequent runs fetch only **new** posts through incremental sync.

```bash
# Disable full sync; only fetch new posts after migration.
FULL_SYNC_ONCE=false
```

#### Re-check after migration

Verify that all migrated files exist on disk with:

```bash
./reddit-downloader --re-check
```

This identifies missing files from the migrated collection.

</details>

### File Reorganization Tool

The separate `migrate` binary reorganizes flat media directories into subreddit-based folder hierarchies. It parses bdfr-html `index.html` files for post metadata and supports dry-run and rollback operations.

```bash
# Dry-run (preview changes)
./migrate --source /path/to/media --dest ./output --index /path/to/index.html --dry-run

# Execute migration
./migrate --source /path/to/media --dest ./output --index /path/to/index.html

# Roll back a completed migration
./migrate --rollback --log-file ./output/.migration_log.json
```

<details>
<summary>File reorganization details</summary>

The output structure is:

```text
output/
├── example_subreddit/                 # Regular subreddit posts
│   └── example_post_title_1r4wjj5.mp4
├── users/                             # User profile posts
│   └── example_user/
│       └── example_post_1r0z7xp.jpeg
└── .migration_log.json                # Migration log for rollback
```

The tool:

1. **Parses** `/path/to/index.html` to extract POSTID→subreddit mapping.
2. **Extracts POSTID** from filenames (`{TITLE}_{POSTID}.{ext}`).
3. **Detects user posts** (subreddits starting with `u_`) and routes them to `users/{username}/`.
4. **Skips orphaned files** that do not match any POSTID in `index.html`.
5. Uses safe file moves with a copy-verify-delete pattern.
6. Creates a JSON log for rollback and audit.

Features include dry-run mode, cross-filesystem support, user profile post detection, comprehensive JSON logging, full rollback support, and handling for orphaned files.

</details>

### Reddit OAuth Setup

<details>
<summary>OAuth2 application walkthrough</summary>

1. Go to https://www.reddit.com/prefs/apps.
2. Click **create another app...**
3. Select **script**.
4. Name: `reddit-upvote-media-downloader`.
5. Description: optional.
6. About URL: optional.
7. Redirect URI: `http://localhost:8080` (not used, but required).
8. Click **create app**.
9. Note the **client ID** (under the app name) and **client secret**.
10. Add them to `.env`.

To generate a refresh token through the local OAuth2 flow, set the client ID and client secret, then run:

```bash
./reddit-downloader --auth
```

The command opens a browser window for authorization and prints a masked token reference. Store the resulting refresh token securely in `REDDIT_REFRESH_TOKEN`; do not commit it or share it publicly.

</details>

### Project Structure

```text
reddit-upvote-media-downloader/
├── cmd/
│   ├── downloader/
│   │   └── main.go              # Main downloader entry point
│   └── migrate/
│       └── main.go              # File reorganization tool
├── internal/
│   ├── config/                  # Configuration
│   ├── reddit/                  # Reddit API client
│   ├── downloader/              # Media download logic
│   ├── storage/                 # SQLite database
│   └── migration/               # File reorganization library
│       ├── extractor.go         # POSTID extraction
│       ├── parser.go            # HTML parsing
│       ├── migrator.go          # Migration logic
│       └── rollback.go           # Rollback functionality
├── Dockerfile                   # Multi-stage build
├── docker-compose.yml           # Docker Compose config
├── .env.example                 # Environment template
└── README.md                    # This file
```

### Troubleshooting

#### Docker image will not build

- Ensure Docker is installed and running.
- Check that `go.mod` and `go.sum` exist.

#### Authentication fails

- Verify your Reddit credentials in `.env`.
- Check that your Reddit app is configured as **script** type.
- Ensure your username/password or refresh token is correct.

#### Downloads fail

- Set `LOG_LEVEL=debug` for detailed logs.
- Verify that you have disk space in `OUTPUT_DIR`.
- Check network connectivity to Reddit and external sites.

#### Migration issues

- Ensure `idList.txt` is in the data directory.
- Check that media files are in `data/output/`.
- Set `MIGRATE_ON_START=true` for the first run.

## Building

### Prerequisites

- Go 1.23 or later for local builds.
- A C compiler and SQLite development libraries for the CGO-backed SQLite driver. On Alpine, install `gcc`, `musl-dev`, and `sqlite-dev`.
- Docker, if building the container image.

### Build the binaries

Build the downloader and migration tools from the repository root:

```bash
go build -o reddit-downloader cmd/downloader/main.go
go build -o migrate cmd/migrate/main.go
```

Run the downloader binary with:

```bash
./reddit-downloader
```

### Build the Docker image

The Dockerfile uses a multi-stage `golang:1.23-alpine` build and an Alpine runtime of approximately 15 MB. Build it with:

```bash
docker build -t reddit-upvote-media-downloader .
```

Or rebuild and start the Compose service:

```bash
docker compose up --build -d
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow, Conventional Commits, testing, linting, release process, and pull request guidelines.

## Acknowledgements

Inspired by [bdfr-html](https://github.com/BlipRanger/bdfr-html) and [bulk-downloader-for-reddit](https://github.com/aliparlakci/bulk-downloader-for-reddit).

## License

[MIT](LICENSE) © 2026 David Dembeck
