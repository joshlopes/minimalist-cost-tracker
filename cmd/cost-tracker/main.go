// Command cost-tracker is the single binary behind the minimalist cost
// tracker. Beyond the runtime modes (`hook`, `serve`, `migrate`) it also owns
// its own lifecycle: `install-hooks` wires it into Claude Code, `service`
// runs the dashboard on login, and `update` self-upgrades from the latest
// GitHub release — so the curl|sh installer needs nothing but this binary.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/lendable/minimalist-cost-tracker/internal/db"
	"github.com/lendable/minimalist-cost-tracker/internal/hook"
	"github.com/lendable/minimalist-cost-tracker/internal/pricing"
	"github.com/lendable/minimalist-cost-tracker/internal/recorder"
	"github.com/lendable/minimalist-cost-tracker/internal/selfupdate"
	"github.com/lendable/minimalist-cost-tracker/internal/service"
	"github.com/lendable/minimalist-cost-tracker/internal/settings"
	"github.com/lendable/minimalist-cost-tracker/internal/web"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

const defaultPort = 7842

const usage = `cost-tracker — track Claude Code session costs

Usage:
  cost-tracker hook                 read a hook event from stdin and record it
  cost-tracker serve [--port N]     start the dashboard (default port 7842)
  cost-tracker migrate              create/upgrade the database schema
  cost-tracker install-hooks [--all] wire the hooks into Claude Code settings.json
                                    (--all targets every profile: ~/.claude and ~/.claude-work)
  cost-tracker service <cmd>        manage the login service:
                                    install|uninstall|start|stop|restart|status
  cost-tracker update [--repo R]    self-update to the latest GitHub release
                                    (--check only reports if a newer one exists)
  cost-tracker version              print version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "hook":
		runHook(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	case "migrate":
		runMigrate()
	case "install-hooks":
		runInstallHooks(os.Args[2:])
	case "service":
		runService(os.Args[2:])
	case "update", "self-update":
		runUpdate(os.Args[2:])
	case "free-port":
		runFreePort(os.Args[2:])
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

// selfPath returns the absolute path to this executable, used when writing
// hooks and service definitions so they survive a moved working directory.
func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "cost-tracker"
	}
	if abs, err := filepath.Abs(exe); err == nil {
		return abs
	}
	return exe
}

// runHook records one event from stdin. It logs to hook.log and always exits 0
// so a tracker failure never breaks a Claude Code session. --profile names the
// Claude Code config the hook was installed under (default "default"), so the
// dashboard can report per-profile.
func runHook(args []string) {
	fs := flag.NewFlagSet("hook", flag.ExitOnError)
	profile := fs.String("profile", "default", "profile label to attribute this session to")
	_ = fs.Parse(args)

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

	rec := recorder.New(database, *profile)
	_ = hook.Handle(os.Stdin, rec, pricing.New())
	os.Exit(0)
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", defaultPort, "port to serve the dashboard on")
	_ = fs.Parse(args)

	if err := checkPortFree(*port); err != nil {
		log.Fatalf("serve: port %d unavailable: %v", *port, err)
	}

	database, err := openDB()
	if err != nil {
		log.Fatalf("serve: open db: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		log.Fatalf("serve: migrate: %v", err)
	}

	srv := web.New(database, pricing.New(), *port, version, selfupdate.DefaultRepo)
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

func runInstallHooks(args []string) {
	fs := flag.NewFlagSet("install-hooks", flag.ExitOnError)
	settingsPath := fs.String("settings", "", "path to a specific Claude Code settings.json (profile inferred from its dir)")
	all := fs.Bool("all", false, "wire hooks into every detected profile (~/.claude and ~/.claude-work)")
	_ = fs.Parse(args)

	profiles, err := installTargets(*settingsPath, *all)
	if err != nil {
		log.Fatalf("install-hooks: %v", err)
	}

	for _, p := range profiles {
		added, err := settings.InstallHooks(p.Path, selfPath(), p.Name)
		if err != nil {
			log.Fatalf("install-hooks: %v", err)
		}
		if len(added) == 0 {
			fmt.Printf("[%s] hooks already present in %s\n", p.Name, p.Path)
		} else {
			fmt.Printf("[%s] wired hooks (%v) into %s\n", p.Name, added, p.Path)
		}
	}
}

// installTargets resolves which profile(s) install-hooks should write to:
// an explicit --settings path wins, then --all (every detected profile), then
// the single auto-detected default.
func installTargets(settingsPath string, all bool) ([]settings.Profile, error) {
	switch {
	case settingsPath != "":
		dir := filepath.Dir(settingsPath)
		return []settings.Profile{{
			Name: settings.ProfileName(dir),
			Dir:  dir,
			Path: settingsPath,
		}}, nil
	case all:
		return settings.AllProfiles()
	default:
		p, err := settings.DefaultProfile()
		if err != nil {
			return nil, err
		}
		return []settings.Profile{p}, nil
	}
}

func runService(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: cost-tracker service <install|uninstall|start|stop|restart|status> [--port N]")
		os.Exit(2)
	}
	sub := args[0]
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	port := fs.Int("port", defaultPort, "port the dashboard service listens on")
	_ = fs.Parse(args[1:])

	switch sub {
	case "install":
		if err := service.Install(selfPath(), *port); err != nil {
			log.Fatalf("service install: %v", err)
		}
		fmt.Printf("service installed; dashboard will run on http://localhost:%d\n", *port)
	case "uninstall":
		if err := service.Uninstall(); err != nil {
			log.Fatalf("service uninstall: %v", err)
		}
		fmt.Println("service removed")
	case "start":
		started, err := service.Start()
		if err != nil {
			log.Fatalf("service start: %v", err)
		}
		if !started {
			fmt.Println("no dashboard service installed; install it with `cost-tracker service install`")
			return
		}
		fmt.Println("dashboard service started")
	case "stop":
		stopped, err := service.Stop()
		if err != nil {
			log.Fatalf("service stop: %v", err)
		}
		if !stopped {
			fmt.Println("no dashboard service installed; nothing to stop")
			return
		}
		fmt.Println("dashboard service stopped")
	case "restart":
		restarted, err := service.Restart()
		if err != nil {
			log.Fatalf("service restart: %v", err)
		}
		if !restarted {
			fmt.Println("no dashboard service installed; install it with `cost-tracker service install`")
			return
		}
		fmt.Println("dashboard service restarted")
	case "status":
		s, err := service.Status()
		if err != nil {
			log.Fatalf("service status: %v", err)
		}
		fmt.Println(s)
	default:
		fmt.Fprintf(os.Stderr, "unknown service command %q\n", sub)
		os.Exit(2)
	}
}

func runUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	repo := fs.String("repo", selfupdate.DefaultRepo, "GitHub owner/name to update from")
	check := fs.Bool("check", false, "only report whether a newer release exists; do not update")
	_ = fs.Parse(args)

	if *check {
		latest, err := selfupdate.LatestVersion(*repo)
		if err != nil {
			log.Fatalf("update: %v", err)
		}
		if selfupdate.IsNewer(version, latest) {
			fmt.Printf("a newer version is available: %s (current %s)\n", latest, version)
			fmt.Println("run `cost-tracker update` to upgrade.")
		} else {
			fmt.Printf("up to date (%s)\n", version)
		}
		return
	}

	newVersion, updated, err := selfupdate.Run(*repo, version, selfPath(), os.Stdout)
	if err != nil {
		log.Fatalf("update: %v", err)
	}
	if !updated {
		fmt.Printf("already up to date (%s)\n", version)
		return
	}
	fmt.Printf("updated %s -> %s\n", version, newVersion)

	// Bounce the login service so the running dashboard stops and restarts on
	// the new binary, keeping its existing port — otherwise the old version
	// keeps holding it and a fresh start would land on a different port.
	restarted, err := service.Restart()
	switch {
	case err != nil:
		fmt.Printf("warning: could not restart the dashboard service: %v\n", err)
		fmt.Println("restart the dashboard (or its service) to run the new version.")
	case restarted:
		fmt.Println("restarted the dashboard service on the new version.")
	default:
		fmt.Println("restart the dashboard to run the new version.")
	}
}

// runFreePort prints the first free TCP port at or above --start, so the
// installer can pick a dashboard port without fragile shell port probing. It
// is intentionally undocumented in the usage text (an installer helper).
func runFreePort(args []string) {
	fs := flag.NewFlagSet("free-port", flag.ExitOnError)
	start := fs.Int("start", defaultPort, "lowest port to consider")
	_ = fs.Parse(args)

	for p := *start; p < *start+100 && p <= 65535; p++ {
		if checkPortFree(p) == nil {
			fmt.Println(p)
			return
		}
	}
	log.Fatalf("free-port: no free port found in [%d, %d)", *start, *start+100)
}

// checkPortFree returns nil if a TCP listener can bind the port, otherwise an
// error explaining it is in use. It binds and immediately closes.
func checkPortFree(port int) error {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	return ln.Close()
}
