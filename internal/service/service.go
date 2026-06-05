// Package service installs cost-tracker's dashboard as a user-level service
// that starts on login and restarts on crash: a LaunchAgent on macOS, a
// systemd user unit on Linux. It is deliberately user-scoped (no sudo) so the
// curl|sh installer never needs elevated privileges.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Label / unit name used across both platforms.
const (
	launchdLabel = "com.cost-tracker.dashboard"
	systemdUnit  = "cost-tracker.service"
)

// Supported reports whether this OS has a service backend.
func Supported() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

// Install writes the platform service definition for "<binPath> serve --port
// <port>" and (re)loads it so the dashboard runs now and on every login. It is
// idempotent: an existing definition is replaced.
func Install(binPath string, port int) error {
	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(binPath, port)
	case "linux":
		return installSystemd(binPath, port)
	default:
		return fmt.Errorf("no service backend for %s; run `cost-tracker serve` manually", runtime.GOOS)
	}
}

// Uninstall stops and removes the service definition. A missing definition is
// not an error.
func Uninstall() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchd()
	case "linux":
		return uninstallSystemd()
	default:
		return fmt.Errorf("no service backend for %s", runtime.GOOS)
	}
}

// Restart stops the running service and starts it again so it picks up a
// replaced binary while keeping its configured port. It reports whether an
// installed service was found to restart; a missing service is not an error
// (the caller falls back to asking the user to restart manually).
func Restart() (bool, error) {
	switch runtime.GOOS {
	case "darwin":
		return restartLaunchd()
	case "linux":
		return restartSystemd()
	default:
		return false, nil
	}
}

// Stop stops the running dashboard service without removing its definition, so
// it can be started again with `service start`/`restart` (and still auto-starts
// on next login). It reports whether an installed service was found; a missing
// service is not an error.
func Stop() (bool, error) {
	switch runtime.GOOS {
	case "darwin":
		return stopLaunchd()
	case "linux":
		return stopSystemd()
	default:
		return false, nil
	}
}

// Start starts an installed-but-stopped service. It reports whether an installed
// service was found; a missing service is not an error (install it first).
func Start() (bool, error) {
	switch runtime.GOOS {
	case "darwin":
		return startLaunchd()
	case "linux":
		return startSystemd()
	default:
		return false, nil
	}
}

// Status returns a human-readable status line, or an error if no service is
// installed.
func Status() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return statusLaunchd()
	case "linux":
		return statusSystemd()
	default:
		return "", fmt.Errorf("no service backend for %s", runtime.GOOS)
	}
}

// --- macOS / launchd -------------------------------------------------------

func launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

func installLaunchd(binPath string, port int) error {
	path, err := launchdPlistPath()
	if err != nil {
		return err
	}
	logDir, err := serviceLogPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
    <string>--port</string>
    <string>%d</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, launchdLabel, binPath, port, logDir, logDir)

	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}

	// Reload: bootout first (ignore errors if not loaded), then bootstrap.
	target := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", target+"/"+launchdLabel).Run()
	if out, err := exec.Command("launchctl", "bootstrap", target, path).CombinedOutput(); err != nil {
		// Fall back to the legacy load verb on older macOS.
		if out2, err2 := exec.Command("launchctl", "load", "-w", path).CombinedOutput(); err2 != nil {
			return fmt.Errorf("launchctl bootstrap failed: %s / %s", out, out2)
		}
	}
	return nil
}

func uninstallLaunchd() error {
	path, err := launchdPlistPath()
	if err != nil {
		return err
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)
	_ = exec.Command("launchctl", "bootout", target).Run()
	_ = exec.Command("launchctl", "unload", path).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// restartLaunchd bounces the LaunchAgent (bootout then bootstrap) so the new
// binary at the same path takes over the same port. A not-installed agent
// returns (false, nil).
func restartLaunchd() (bool, error) {
	path, err := launchdPlistPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	target := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", target+"/"+launchdLabel).Run()
	if out, err := exec.Command("launchctl", "bootstrap", target, path).CombinedOutput(); err != nil {
		// Fall back to the legacy load verb on older macOS.
		if out2, err2 := exec.Command("launchctl", "load", "-w", path).CombinedOutput(); err2 != nil {
			return true, fmt.Errorf("launchctl bootstrap failed: %s / %s", out, out2)
		}
	}
	return true, nil
}

// stopLaunchd boots out the running agent but leaves the plist in place. A
// not-installed agent returns (false, nil).
func stopLaunchd() (bool, error) {
	path, err := launchdPlistPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)
	_ = exec.Command("launchctl", "bootout", target).Run()
	return true, nil
}

// startLaunchd bootstraps the agent from its existing plist. A not-installed
// agent returns (false, nil).
func startLaunchd() (bool, error) {
	path, err := launchdPlistPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	target := fmt.Sprintf("gui/%d", os.Getuid())
	if out, err := exec.Command("launchctl", "bootstrap", target, path).CombinedOutput(); err != nil {
		// Fall back to the legacy load verb on older macOS.
		if out2, err2 := exec.Command("launchctl", "load", "-w", path).CombinedOutput(); err2 != nil {
			return true, fmt.Errorf("launchctl bootstrap failed: %s / %s", out, out2)
		}
	}
	return true, nil
}

func statusLaunchd() (string, error) {
	path, err := launchdPlistPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("not installed (no %s)", path)
	}
	out, err := exec.Command("launchctl", "list", launchdLabel).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("installed at %s but not loaded", path), nil
	}
	return fmt.Sprintf("installed and loaded (%s)\n%s", path, out), nil
}

// --- Linux / systemd (user) ------------------------------------------------

func systemdUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", systemdUnit), nil
}

func installSystemd(binPath string, port int) error {
	path, err := systemdUnitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=cost-tracker dashboard
After=network.target

[Service]
ExecStart=%s serve --port %d
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`, binPath, port)

	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("wrote %s but systemctl not found; enable it manually", path)
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", systemdUnit).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user enable --now %s: %s", systemdUnit, out)
	}
	return nil
}

func uninstallSystemd() error {
	path, err := systemdUnitPath()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("systemctl"); err == nil {
		_ = exec.Command("systemctl", "--user", "disable", "--now", systemdUnit).Run()
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// restartSystemd restarts the user unit so the new binary at the same path
// takes over the same port. A not-installed unit returns (false, nil).
func restartSystemd() (bool, error) {
	path, err := systemdUnitPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return true, fmt.Errorf("systemctl not found; restart %s manually", systemdUnit)
	}
	if out, err := exec.Command("systemctl", "--user", "restart", systemdUnit).CombinedOutput(); err != nil {
		return true, fmt.Errorf("systemctl --user restart %s: %s", systemdUnit, out)
	}
	return true, nil
}

// stopSystemd stops the running user unit but leaves it enabled, so it restarts
// on next login. A not-installed unit returns (false, nil).
func stopSystemd() (bool, error) {
	path, err := systemdUnitPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return true, fmt.Errorf("systemctl not found; stop %s manually", systemdUnit)
	}
	if out, err := exec.Command("systemctl", "--user", "stop", systemdUnit).CombinedOutput(); err != nil {
		return true, fmt.Errorf("systemctl --user stop %s: %s", systemdUnit, out)
	}
	return true, nil
}

// startSystemd starts an installed-but-stopped user unit. A not-installed unit
// returns (false, nil).
func startSystemd() (bool, error) {
	path, err := systemdUnitPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return true, fmt.Errorf("systemctl not found; start %s manually", systemdUnit)
	}
	if out, err := exec.Command("systemctl", "--user", "start", systemdUnit).CombinedOutput(); err != nil {
		return true, fmt.Errorf("systemctl --user start %s: %s", systemdUnit, out)
	}
	return true, nil
}

func statusSystemd() (string, error) {
	path, err := systemdUnitPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("not installed (no %s)", path)
	}
	out, _ := exec.Command("systemctl", "--user", "is-active", systemdUnit).CombinedOutput()
	return fmt.Sprintf("installed at %s (is-active: %s)", path, string(out)), nil
}

// serviceLogPath returns the file launchd should redirect stdout/stderr to.
func serviceLogPath() (string, error) {
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
	return filepath.Join(dir, "service.log"), nil
}
