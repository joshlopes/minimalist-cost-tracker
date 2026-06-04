// Command cost-tracker is the single binary behind the minimalist cost
// tracker. It runs in three modes: `hook` (invoked by Claude Code per event),
// `serve` (the dashboard), and `migrate` (schema setup, used by setup.sh).
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/lendable/minimalist-cost-tracker/internal/db"
	"github.com/lendable/minimalist-cost-tracker/internal/hook"
	"github.com/lendable/minimalist-cost-tracker/internal/pricing"
	"github.com/lendable/minimalist-cost-tracker/internal/recorder"
	"github.com/lendable/minimalist-cost-tracker/internal/web"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

const usage = `cost-tracker — track Claude Code session costs

Usage:
  cost-tracker hook              read a hook event from stdin and record it
  cost-tracker serve [--port N]  start the dashboard (default port 7842)
  cost-tracker migrate           create/upgrade the database schema
  cost-tracker version           print version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "hook":
		runHook()
	case "serve":
		runServe(os.Args[2:])
	case "migrate":
		runMigrate()
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// dataDir returns the cost-tracker data directory, honouring XDG_DATA_HOME and
// creating it if necessary.
func dataDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(base, "cost-tracker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func dbPath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tracker.db"), nil
}

func openDB() (*db.DB, error) {
	path, err := dbPath()
	if err != nil {
		return nil, err
	}
	return db.Open(path)
}

// runHook records one event from stdin. It logs to hook.log and always exits 0
// so a tracker failure never breaks a Claude Code session.
func runHook() {
	if dir, err := dataDir(); err == nil {
		if f, err := os.OpenFile(filepath.Join(dir, "hook.log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			log.SetOutput(f)
			defer f.Close()
		}
	}

	database, err := openDB()
	if err != nil {
		log.Printf("hook: open db: %v", err)
		os.Exit(0)
	}
	defer database.Close()

	rec := recorder.New(database)
	_ = hook.Handle(os.Stdin, rec, pricing.New())
	os.Exit(0)
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 7842, "port to serve the dashboard on")
	_ = fs.Parse(args)

	database, err := openDB()
	if err != nil {
		log.Fatalf("serve: open db: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		log.Fatalf("serve: migrate: %v", err)
	}

	srv := web.New(database, pricing.New(), *port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func runMigrate() {
	database, err := openDB()
	if err != nil {
		log.Fatalf("migrate: open db: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	fmt.Println("schema up to date")
}
