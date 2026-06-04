// Package selfupdate replaces the running cost-tracker binary with the latest
// release published on GitHub. It talks only to the public releases API and
// the release asset URLs — no auth, no extra tooling — so `cost-tracker
// update` mirrors what install.sh does on first install.
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultRepo is the GitHub "owner/name" releases are pulled from. It can be
// overridden at build time with -ldflags "-X ...selfupdate.DefaultRepo=...".
var DefaultRepo = "joshlopes/minimalist-cost-tracker"

const apiBase = "https://api.github.com"

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// AssetName is the tarball published for a given GOOS/GOARCH. install.sh and
// the release workflow must agree on this exact convention.
func AssetName(goos, goarch string) string {
	return fmt.Sprintf("cost-tracker_%s_%s.tar.gz", goos, goarch)
}

// Run checks the latest release for repo and, if it differs from current,
// downloads the matching asset and replaces the executable at execPath. It
// returns the version it ended up on and whether an update was applied.
func Run(repo, current, execPath string, out io.Writer) (string, bool, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	rel, err := latest(repo)
	if err != nil {
		return "", false, err
	}
	if rel.TagName == "" {
		return "", false, fmt.Errorf("no published releases for %s", repo)
	}
	if normalize(rel.TagName) == normalize(current) {
		return rel.TagName, false, nil
	}

	want := AssetName(runtime.GOOS, runtime.GOARCH)
	var assetURL, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			assetURL = a.URL
		case "SHA256SUMS":
			sumsURL = a.URL
		}
	}
	if assetURL == "" {
		return "", false, fmt.Errorf("release %s has no asset %q (unsupported platform %s/%s?)",
			rel.TagName, want, runtime.GOOS, runtime.GOARCH)
	}

	fmt.Fprintf(out, "downloading %s %s...\n", want, rel.TagName)
	tarball, err := download(assetURL)
	if err != nil {
		return "", false, err
	}

	if sumsURL != "" {
		if err := verifyChecksum(tarball, want, sumsURL); err != nil {
			return "", false, err
		}
	}

	bin, err := extractBinary(tarball)
	if err != nil {
		return "", false, err
	}

	if err := replaceExecutable(execPath, bin); err != nil {
		return "", false, err
	}
	return rel.TagName, true, nil
}

func latest(repo string) (*release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", apiBase, repo)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no releases found for %s (yet?)", repo)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github api %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func download(url string) ([]byte, error) {
	resp, err := httpClient().Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// verifyChecksum fetches SHA256SUMS and confirms the tarball's digest matches
// the line for assetName.
func verifyChecksum(tarball []byte, assetName, sumsURL string) error {
	raw, err := download(sumsURL)
	if err != nil {
		return fmt.Errorf("fetch checksums: %w", err)
	}
	sum := sha256.Sum256(tarball)
	got := hex.EncodeToString(sum[:])

	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// sha256sum format: "<hex>  <name>" (name may be "*name").
		name := strings.TrimPrefix(fields[1], "*")
		if name == assetName {
			if !strings.EqualFold(fields[0], got) {
				return fmt.Errorf("checksum mismatch for %s: got %s, want %s", assetName, got, fields[0])
			}
			return nil
		}
	}
	return fmt.Errorf("no checksum entry for %s in SHA256SUMS", assetName)
}

// extractBinary pulls the cost-tracker file out of the gzipped tarball.
func extractBinary(tarball []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) == "cost-tracker" && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("cost-tracker binary not found in release archive")
}

// replaceExecutable writes bin next to execPath and renames it into place,
// which is atomic on the same filesystem and safe while the old binary runs.
func replaceExecutable(execPath string, bin []byte) error {
	resolved, err := filepath.EvalSymlinks(execPath)
	if err == nil {
		execPath = resolved
	}
	dir := filepath.Dir(execPath)
	tmp, err := os.CreateTemp(dir, ".cost-tracker-new-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (permission?): %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, execPath); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func httpClient() *http.Client { return &http.Client{Timeout: 60 * time.Second} }

// normalize strips a leading "v" and surrounding space so "v1.2.0" and
// "1.2.0" compare equal.
func normalize(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
