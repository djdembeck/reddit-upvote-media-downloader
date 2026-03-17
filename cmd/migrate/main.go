package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/djdembeck/reddit-upvote-media-downloader/internal/migration"
	"github.com/djdembeck/reddit-upvote-media-downloader/internal/storage"
)

//nolint:gocritic // os.Exit does not run deferred functions, but closeDB() is called explicitly before exit
func exitWithCloseDB(closeDB func(), code int) {
	closeDB()
	os.Exit(code)
}

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
		if err := handleRollback(logFile, sourceDir, destDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := validateFlags(*sourceDir, *destDir, *indexPath, *htmlDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.PrintDefaults()
		os.Exit(1)
	}

	if err := runMigration(*sourceDir, *destDir, *indexPath, *htmlDir, *logFile, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleRollback(logFile *string, sourceDir, destDir *string) error {
	if *logFile == "" {
		return fmt.Errorf("--log-file required for rollback")
	}
	logSourceRoot, logDestRoot, err := readRootsFromLog(*logFile)
	if err != nil {
		return err
	}
	if *sourceDir == "" {
		*sourceDir = logSourceRoot
	}
	if *destDir == "" {
		*destDir = logDestRoot
	}
	if *sourceDir == "" || *destDir == "" {
		return fmt.Errorf("--source and --dest are required when migration log is missing source_dir or dest_dir")
	}
	runRollback(*logFile, *sourceDir, *destDir)
	return nil
}

func validateFlags(sourceDir, destDir, indexPath, htmlDir string) error {
	if sourceDir == "" || destDir == "" {
		return fmt.Errorf("Error: --source and --dest are required")
	}
	if indexPath == "" && htmlDir == "" {
		return fmt.Errorf("Error: either --index or --html-dir is required")
	}
	if indexPath != "" && htmlDir != "" {
		return fmt.Errorf("Error: cannot use both --index and --html-dir")
	}
	return nil
}

func runMigration(sourceDir, destDir, indexPath, htmlDir, logFile string, dryRun bool) error {
	ctx := context.Background()

	printMigrationHeader(sourceDir, destDir, htmlDir, indexPath, dryRun)

	parser, err := parseMigrationMetadata(ctx, htmlDir, indexPath)
	if err != nil {
		return err
	}
	fmt.Printf("Found %d posts\n\n", len(parser.PostMap))

	db, closeDB, err := initMigrationDB(dryRun)
	if err != nil {
		return err
	}
	defer closeDB()

	if !dryRun {
		if err := os.MkdirAll(destDir, 0750); err != nil {
			return fmt.Errorf("create destination directory: %w", err)
		}
	}

	logFile = resolveLogFile(logFile, destDir, dryRun)

	migrator, err := executeMigration(ctx, sourceDir, destDir, parser.PostMap, dryRun, db, logFile)
	if err != nil {
		return err
	}

	printMigrationSummary(migrator, logFile, dryRun)

	if migrator.Log.ErrorCount > 0 {
		return fmt.Errorf("migration completed with %d errors", migrator.Log.ErrorCount)
	}
	return nil
}

func printMigrationHeader(sourceDir, destDir, htmlDir, indexPath string, dryRun bool) {
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
}

func parseMigrationMetadata(ctx context.Context, htmlDir, indexPath string) (*migration.HTMLParser, error) {
	parser := migration.NewHTMLParser()
	if htmlDir != "" {
		fmt.Println("Parsing HTML files...")
		if err := parser.ParseHTMLFiles(ctx, htmlDir); err != nil {
			return nil, fmt.Errorf("parse html files: %w", err)
		}
	} else {
		fmt.Println("Parsing index.html...")
		if err := parser.ParseIndexHTML(ctx, indexPath); err != nil {
			return nil, fmt.Errorf("parse index html: %w", err)
		}
	}
	return parser, nil
}

func initMigrationDB(dryRun bool) (*storage.DB, func(), error) {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" || dryRun {
		return nil, func() {}, nil
	}

	fmt.Printf("Initializing database: %s\n", dbPath)
	db, err := storage.NewDB(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	closeFn := func() {
		if err := db.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing database: %v\n", err)
		}
	}
	return db, closeFn, nil
}

func resolveLogFile(logFile, destDir string, dryRun bool) string {
	if logFile != "" {
		return logFile
	}
	if dryRun {
		return filepath.Join(os.TempDir(), ".migration_log.json")
	}
	return filepath.Join(destDir, ".migration_log.json")
}

func executeMigration(ctx context.Context, sourceDir, destDir string, postMap map[string]migration.PostInfo, dryRun bool, db *storage.DB, logFile string) (*migration.Migrator, error) {
	migrator := migration.NewMigrator(sourceDir, destDir, postMap, dryRun, db)
	if err := migrator.LoadExistingLog(ctx, logFile); err != nil {
		return nil, fmt.Errorf("load existing log: %w", err)
	}

	if err := migrator.Execute(ctx); err != nil {
		return nil, fmt.Errorf("executing migration: %w", err)
	}

	if err := migrator.SaveLog(ctx, logFile); err != nil {
		return nil, fmt.Errorf("migration completed but failed to save log: %w", err)
	}

	return migrator, nil
}

func printMigrationSummary(migrator *migration.Migrator, logFile string, dryRun bool) {
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
}

func runRollback(logPath, sourceRoot, destRoot string) {
	printRollbackHeader(logPath)

	db, closeDB := initRollbackDB()
	defer closeDB()

	if err := validateAndExecuteRollback(logPath, sourceRoot, destRoot, db, closeDB); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		exitWithCloseDB(closeDB, 1)
	}
}

func printRollbackHeader(logPath string) {
	fmt.Println("Rollback")
	fmt.Println("========")
	fmt.Printf("Log: %s\n\n", logPath)
}

func initRollbackDB() (*storage.DB, func()) {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		return nil, func() {}
	}

	fmt.Printf("Initializing database: %s\n", dbPath)
	db, err := storage.NewDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	closeFn := func() {
		if err := db.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing database: %v\n", err)
		}
	}
	return db, closeFn
}

func validateAndExecuteRollback(logPath, sourceRoot, destRoot string, db *storage.DB, closeDB func()) error {
	logSourceRoot, logDestRoot, err := readRootsFromLog(logPath)
	if err != nil {
		return err
	}

	if err := validateRollbackRoots(logSourceRoot, logDestRoot, sourceRoot, destRoot); err != nil {
		return err
	}

	return executeRollbackAndPrint(logPath, db, sourceRoot, destRoot)
}

func validateRollbackRoots(logSourceRoot, logDestRoot, sourceRoot, destRoot string) error {
	if logSourceRoot != "" && logSourceRoot != sourceRoot {
		return fmt.Errorf("log source directory (%s) does not match CLI source directory (%s)", logSourceRoot, sourceRoot)
	}
	if logDestRoot != "" && logDestRoot != destRoot {
		return fmt.Errorf("log destination directory (%s) does not match CLI destination directory (%s)", logDestRoot, destRoot)
	}
	return nil
}

func executeRollbackAndPrint(logPath string, db *storage.DB, sourceRoot, destRoot string) error {
	rb := migration.NewRollback(logPath, db, sourceRoot, destRoot)
	rollbackLog, err := rb.Execute(context.Background())
	if err != nil {
		return err
	}

	rollbackPath := logPath + ".rollback_" + time.Now().Format("20060102_150405") + ".json"
	if err := migration.SaveRollbackLog(rollbackLog, rollbackPath); err != nil {
		return fmt.Errorf("saving rollback log: %w", err)
	}

	printRollbackSummary(rollbackLog, rollbackPath)

	if rollbackLog.ErrorCount > 0 {
		return fmt.Errorf("rollback completed with %d errors", rollbackLog.ErrorCount)
	}
	return nil
}

func printRollbackSummary(rollbackLog *migration.RollbackLog, rollbackPath string) {
	fmt.Println("Rollback Summary")
	fmt.Println("================")
	fmt.Printf("Success: %d\n", rollbackLog.SuccessCount)
	fmt.Printf("Errors: %d\n", rollbackLog.ErrorCount)
	fmt.Printf("Log: %s\n", rollbackPath)
}

func readRootsFromLog(logPath string) (string, string, error) {
	data, err := os.ReadFile(filepath.Clean(logPath))
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
