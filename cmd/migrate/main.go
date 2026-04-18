// migrate is a tool for reorganizing Reddit media files from flat directory
// structures into subreddit-based folder hierarchies. It parses bdfr-html
// index.html files to extract post metadata and moves files accordingly.
//
// Usage:
//
//	./migrate --source /path/to/media --dest ./output --index /path/to/index.html
//
// The tool supports dry-run mode, rollback functionality, and creates JSON
// logs for audit and recovery purposes.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/migration"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/ownutil"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
)

//nolint:cyclop
func main() {
	var (
		sourceDir = flag.String("source", "", "Source media directory (required)")
		destDir   = flag.String("dest", "", "Destination output directory (required)")
		indexPath = flag.String("index", "", "Path to index.html (required)")
		htmlDir   = flag.String("html-dir", "", "Path to directory containing HTML files (alternative to --index)")
		dryRun    = flag.Bool("dry-run", false, "Preview mode")
		rollback  = flag.Bool("rollback", false, "Rollback mode")
		logFile   = flag.String("log-file", "", "Migration log path")
	)

	flag.Parse()

	if *rollback {
		if *logFile == "" {
			fmt.Fprintln(os.Stderr, "Error: --log-file required for rollback")
			os.Exit(1)
		}

		if *sourceDir == "" || *destDir == "" {
			fmt.Fprintln(os.Stderr, "Error: --source and --dest are required for rollback "+
				"(must be explicitly provided by operator)")
			os.Exit(1)
		}

		logSourceRoot, logDestRoot, err := readRootsFromLog(*logFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if logSourceRoot != "" || logDestRoot != "" {
			fmt.Printf("Log source directory: %s\n", logSourceRoot)
			fmt.Printf("Log destination directory: %s\n", logDestRoot)
		}

		runRollback(*logFile, *sourceDir, *destDir)

		return
	}

	// Validate
	if *sourceDir == "" || *destDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --source and --dest are required")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if *indexPath == "" && *htmlDir == "" {
		fmt.Fprintln(os.Stderr, "Error: either --index or --html-dir is required")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if *indexPath != "" && *htmlDir != "" {
		fmt.Fprintln(os.Stderr, "Error: cannot use both --index and --html-dir")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if err := runMigration(*sourceDir, *destDir, *indexPath, *htmlDir, *logFile, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

//nolint:cyclop,gocyclo
func runMigration(sourceDir, destDir, indexPath, htmlDir, logFile string, dryRun bool) error {
	ctx := context.Background()

	fmt.Println("Reddit Media Migration Tool")
	fmt.Println("==========================")
	fmt.Printf("Source: %s\n", sourceDir)
	fmt.Printf("Destination: %s\n", destDir)

	if htmlDir != "" {
		fmt.Printf("HTML Directory: %s\n", htmlDir)
	} else {
		fmt.Printf("Index: %s\n", indexPath)
	}

	if dryRun {
		fmt.Println("Mode: DRY RUN")
	}

	fmt.Println()

	parser := migration.NewHTMLParser()

	if err := parseHTMLSources(ctx, parser, htmlDir, indexPath); err != nil {
		return err
	}

	fmt.Printf("Found %d posts\n\n", len(parser.PostMap))

	owner, err := newOwnerFromEnv()
	if err != nil {
		return err
	}

	// Initialize DB if DB_PATH is set and not in dry-run mode
	db, _, err := initMigrationDB(ctx, dryRun, owner)
	if err != nil {
		return err
	}
	if db != nil {
		defer func() {
			if err := db.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Error closing database: %v\n", err)
			}
		}()
	}

	if !dryRun {
		if err := owner.ChownMkdirAllContext(ctx, destDir, 0750, slog.Default()); err != nil {
			return fmt.Errorf("create destination directory: %w", err)
		}
	}

	if logFile == "" {
		if dryRun {
			logFile = filepath.Join(os.TempDir(), ".migration_log.json")
		} else {
			logFile = filepath.Join(destDir, ".migration_log.json")
		}
	}

	// Execute
	migrator := setupMigrator(sourceDir, destDir, parser.PostMap, dryRun, db, owner)
	if err := migrator.LoadExistingLog(ctx, logFile); err != nil {
		return fmt.Errorf("load existing log: %w", err)
	}

	if err := migrator.Execute(ctx); err != nil {
		return fmt.Errorf("executing migration: %w", err)
	}

	if err := migrator.SaveLog(ctx, logFile); err != nil {
		return fmt.Errorf("migration completed but failed to save log: %w", err)
	}

	// Summary
	fmt.Println("\nMigration Summary")
	fmt.Println("=================")
	fmt.Printf("Total: %d\n", migrator.Log.TotalFiles)
	fmt.Printf("Moved: %d\n", migrator.Log.MovedCount)
	fmt.Printf("Skipped: %d\n", migrator.Log.SkippedCount)
	fmt.Printf("Warnings: %d\n", migrator.Log.WarningCount)
	fmt.Printf("Errors: %d\n", migrator.Log.ErrorCount)
	fmt.Printf("Log: %s\n", logFile)

	if dryRun {
		fmt.Println("\nDry run complete. Remove --dry-run to execute.")
	}

	if migrator.Log.ErrorCount > 0 {
		return fmt.Errorf("migration completed with %d errors", migrator.Log.ErrorCount)
	}

	return nil
}

func parseHTMLSources(ctx context.Context, parser *migration.HTMLParser, htmlDir, indexPath string) error {
	if htmlDir != "" {
		fmt.Println("Parsing HTML files...")
		if err := parser.ParseHTMLFiles(ctx, htmlDir); err != nil {
			return fmt.Errorf("parse html files: %w", err)
		}
	} else {
		fmt.Println("Parsing index.html...")
		baseDir := filepath.Dir(indexPath)
		if err := parser.ParseIndexHTML(ctx, baseDir, indexPath); err != nil {
			return fmt.Errorf("parse index html: %w", err)
		}
	}
	return nil
}

func initMigrationDB(ctx context.Context, dryRun bool, owner *ownutil.Owner) (*storage.DB, string, error) {
	dbPath := os.Getenv("DB_PATH")

	if dbPath != "" && !dryRun {
		fmt.Printf("Initializing database: %s\n", dbPath)

		db, err := storage.NewDB(ctx, dbPath, owner)
		if err != nil {
			return nil, "", fmt.Errorf("open database: %w", err)
		}

		return db, dbPath, nil
	}

	return nil, dbPath, nil
}

func setupMigrator(sourceDir, destDir string, postMap map[string]migration.PostInfo, dryRun bool, db *storage.DB, owner *ownutil.Owner) *migration.Migrator {
	return migration.NewMigrator(sourceDir, destDir, postMap, dryRun, db, owner)
}

func newOwnerFromEnv() (*ownutil.Owner, error) {
	puidStr := os.Getenv("PUID")
	pgidStr := os.Getenv("PGID")

	puid, puidErr := strconv.Atoi(puidStr)
	if puidErr != nil && puidStr != "" {
		return nil, fmt.Errorf("invalid PUID value %q: %w", puidStr, puidErr)
	}

	pgid, pgidErr := strconv.Atoi(pgidStr)
	if pgidErr != nil && pgidStr != "" {
		return nil, fmt.Errorf("invalid PGID value %q: %w", pgidStr, pgidErr)
	}

	owner, err := ownutil.NewOwner(puid, pgid)
	if err != nil {
		return nil, fmt.Errorf("invalid PUID/PGID: %w", err)
	}

	return owner, nil
}

//nolint:cyclop
func runRollback(logPath, sourceRoot, destRoot string) {
	ctx := context.Background()

	fmt.Println("Rollback")
	fmt.Println("========")
	fmt.Printf("Log: %s\n\n", logPath)

	owner, err := newOwnerFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}

	var db *storage.DB

	dbPath := os.Getenv("DB_PATH")

	if dbPath != "" {
		fmt.Printf("Initializing database: %s\n", dbPath)

		var err error

		db, err = storage.NewDB(ctx, dbPath, owner)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)

			return
		}

		defer func() {
			if db != nil {
				if err := db.Close(); err != nil {
					fmt.Fprintf(os.Stderr, "Error closing database: %v\n", err)
				}
			}
		}()
	}

	rollbacker := migration.NewRollback(logPath, db, sourceRoot, destRoot, owner)

	rollbackLog, err := rollbacker.Execute(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing rollback: %v\n", err)

		return
	}

	rollbackPath := logPath + ".rollback_" + time.Now().Format("20060102_150405") + ".json"
	if err := migration.SaveRollbackLog(rollbackLog, rollbackPath, owner); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving rollback log: %v\n", err)
		return
	}

	fmt.Println("Rollback Summary")
	fmt.Println("================")
	fmt.Printf("Success: %d\n", rollbackLog.SuccessCount)
	fmt.Printf("Errors: %d\n", rollbackLog.ErrorCount)
	fmt.Printf("Log: %s\n", rollbackPath)

	if rollbackLog.ErrorCount > 0 {
		// Close database explicitly before exit to ensure cleanup runs
		// (defers don't run on os.Exit, so we must close explicitly)
		if db != nil {
			if err := db.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Error closing database: %v\n", err)
			}

			db = nil // Prevent double-close in defer
		}

		os.Exit(1) //nolint:gocritic // cleanup done explicitly above
	}
}

func readRootsFromLog(logPath string) (string, string, error) {
	//nolint:gosec // G304: intentional file reading from user-provided path
	data, err := os.ReadFile(logPath)
	if err != nil {
		return "", "", fmt.Errorf("readRootsFromLog: read %s: %w", logPath, err)
	}

	var log struct {
		SourceDir string `json:"sourceDir"`
		DestDir   string `json:"destDir"`
	}

	if err := json.Unmarshal(data, &log); err != nil {
		return "", "", fmt.Errorf("readRootsFromLog: unmarshal %s: %w", logPath, err)
	}

	return log.SourceDir, log.DestDir, nil
}
