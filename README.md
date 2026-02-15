# Reddit Media Downloader

A lightweight, efficient Reddit media downloader written in Go. Fetches upvoted and saved posts, downloads images and videos (including from external sites), and tracks downloads to avoid duplicates. Runs on a 1-hour Docker schedule.

**Replaces bdfr-html** with a 60x smaller Docker image (~15MB vs ~900MB).

## Features

- ✅ OAuth2 authentication with Reddit
- ✅ Fetches both **upvoted** and **saved** posts
- ✅ Downloads from Reddit-hosted and external sources (Imgur, Gfycat, Redgifs)
- ✅ Concurrent downloads (10 parallel by default)
- ✅ SQLite database for deduplication tracking
- ✅ Automatic migration from existing bdfr-html data
- ✅ Minimal Docker image (~15MB)
- ✅ Hourly scheduled execution
- ✅ Graceful shutdown handling

## Quick Start

### Docker (Recommended)

1. Clone and configure:
```bash
cd /home/djdembeck/projects/github/reddit-media-downloader
cp .env.example .env
# Edit .env with your Reddit credentials
```

2. Start the downloader:
```bash
docker-compose up -d
```

### Binary

1. Build:
```bash
go build -o reddit-downloader cmd/downloader/main.go
```

2. Configure environment variables (see Configuration)

3. Run:
```bash
./reddit-downloader
```

## Configuration

Create a `.env` file with the following variables:

```env
# Reddit API Credentials (required)
# Get these from https://www.reddit.com/prefs/apps
REDDIT_CLIENT_ID=your_client_id_here
REDDIT_CLIENT_SECRET=your_client_secret_here
REDDIT_USER_AGENT=script:reddit-media-downloader:v1.0 (by /u/your_username)
REDDIT_USERNAME=your_reddit_username
REDDIT_PASSWORD=your_reddit_password

# Download Settings (optional)
OUTPUT_DIR=./data/output          # Where to save media
DB_PATH=./data/posts.db           # SQLite database path
CONCURRENCY=10                    # Parallel downloads
FETCH_LIMIT=100                   # Posts per fetch

# Logging (optional)
LOG_LEVEL=info                    # debug, info, warn, error

# Migration (optional)
MIGRATE_ON_START=true             # Auto-migrate from bdfr-html
```

## Migration from bdfr-html

The downloader automatically migrates existing bdfr-html data on first run:

1. **Import from idList.txt**: Existing post IDs are imported into SQLite
2. **Scan media files**: Existing media files are discovered and tracked
3. **No re-downloads**: Already downloaded posts are skipped

**To migrate:**
1. Copy your existing bdfr-html output to the new data directory:
   ```bash
   cp -r /path/to/bdfr-html/output/* ./data/output/
   cp /path/to/bdfr-html/output/idList.txt ./data/
   ```
2. Start the downloader with `MIGRATE_ON_START=true`
3. Logs will show: *"Migrated X existing posts from bdfr-html"*

## Reddit OAuth Setup

1. Go to https://www.reddit.com/prefs/apps
2. Click "create another app..."
3. Select "script"
4. Name: `reddit-media-downloader`
5. Description: (optional)
6. About URL: (optional)
7. Redirect URI: `http://localhost:8080` (not used, but required)
8. Click "create app"
9. Note the **client ID** (under the app name) and **client secret**
10. Add to your `.env` file

## Project Structure

```
reddit-media-downloader/
├── cmd/
│   └── downloader/
│       └── main.go              # Entry point
├── internal/
│   ├── config/                  # Configuration
│   ├── reddit/                  # Reddit API client
│   ├── downloader/              # Media download logic
│   └── storage/                 # SQLite database
├── Dockerfile                   # Multi-stage build
├── docker-compose.yml           # Docker Compose config
├── .env.example                 # Environment template
└── README.md                    # This file
```

## Troubleshooting

### Docker image won't build
- Ensure Docker is installed and running
- Check that `go.mod` and `go.sum` exist

### Authentication fails
- Verify your Reddit credentials in `.env`
- Check that your Reddit app is configured as "script" type
- Ensure your username/password are correct

### Downloads fail
- Check `LOG_LEVEL=debug` for detailed logs
- Verify you have disk space in `OUTPUT_DIR`
- Check network connectivity to Reddit and external sites

### Migration issues
- Ensure `idList.txt` is in the data directory
- Check that media files are in `data/output/`
- Set `MIGRATE_ON_START=true` for first run

## Comparison with bdfr-html

| Feature | bdfr-html | This Project |
|---------|-----------|--------------|
| Docker Image | ~900MB | ~15MB (60x smaller) |
| Memory Usage | 100-200MB | 10-20MB |
| Startup Time | 2-5 seconds | <100ms |
| Concurrency | Limited | 10+ parallel downloads |
| HTML Generation | Yes | **No** (not needed) |
| JSON Metadata | Yes | **No** (not needed) |

## License

MIT License - See LICENSE file for details

## Credits

Inspired by [bdfr-html](https://github.com/BlipRanger/bdfr-html) and [bulk-downloader-for-reddit](https://github.com/aliparlakci/bulk-downloader-for-reddit)
