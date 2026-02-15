package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/user/reddit-media-downloader/internal/migration"
)

func main() {
	var (
		sourceDir = flag.String("source", "", "Source media directory (required)")
		destDir   = flag.String("dest", "", "Destination output directory (required)")
		indexPath = flag.String("index", "", "Path to index.html (required)")
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
		runRollback(*logFile)
		return
	}

	// Validate
	if *sourceDir == "" || *destDir == "" || *indexPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: migrate --source <dir> --dest <dir> --index <file> [--dry-run]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Default log path
	if *logFile == "" {
		*logFile = filepath.Join(*destDir, ".migration_log.json")
	}

	runMigration(*sourceDir, *destDir, *indexPath, *logFile, *dryRun)
}

func runMigration(sourceDir, destDir, indexPath, logFile string, dryRun bool) {
	fmt.Println("Reddit Media Migration Tool")
	fmt.Println("==========================")
	fmt.Printf("Source: %s\n", sourceDir)
	fmt.Printf("Destination: %s\n", destDir)
	fmt.Printf("Index: %s\n", indexPath)
	if dryRun {
		fmt.Println("Mode: DRY RUN")
	}
	fmt.Println()

	// Parse index
	fmt.Println("Parsing index.html...")
	parser := migration.NewHTMLParser()
	if err := parser.ParseIndexHTML(indexPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Found %d posts\n\n", len(parser.PostMap))

	// Execute
	migrator := migration.NewMigrator(sourceDir, destDir, parser.PostMap, dryRun)
	if err := migrator.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Save log
	if err := migrator.SaveLog(logFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving log: %v\n", err)
		os.Exit(1)
	}

	// Summary
	fmt.Println("\nMigration Summary")
	fmt.Println("=================")
	fmt.Printf("Total: %d\n", migrator.Log.TotalFiles)
	fmt.Printf("Moved: %d\n", migrator.Log.MovedCount)
	fmt.Printf("Skipped: %d\n", migrator.Log.SkippedCount)
	fmt.Printf("Errors: %d\n", migrator.Log.ErrorCount)
	fmt.Printf("Log: %s\n", logFile)

	if dryRun {
		fmt.Println("\nDry run complete. Remove --dry-run to execute.")
	}

	if migrator.Log.ErrorCount > 0 {
		os.Exit(1)
	}
}

func runRollback(logPath string) {
	fmt.Println("Rollback")
	fmt.Println("========")
	fmt.Printf("Log: %s\n\n", logPath)

	rb := migration.NewRollback(logPath)
	rollbackLog, err := rb.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Save rollback log
	rollbackPath := logPath + ".rollback_" + time.Now().Format("20060102_150405") + ".json"
	if err := migration.SaveRollbackLog(rollbackLog, rollbackPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving rollback log: %v\n", err)
	}

	fmt.Println("Rollback Summary")
	fmt.Println("================")
	fmt.Printf("Success: %d\n", rollbackLog.SuccessCount)
	fmt.Printf("Errors: %d\n", rollbackLog.ErrorCount)
	fmt.Printf("Log: %s\n", rollbackPath)

	if rollbackLog.ErrorCount > 0 {
		os.Exit(1)
	}
}
