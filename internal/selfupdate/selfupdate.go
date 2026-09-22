// Package selfupdate replaces the running shelf binary with the latest GitHub release.
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
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
)

// apiBase is the GitHub API root; a variable so tests can point it at a fake server.
var apiBase = "https://api.github.com"

// client sends a minimal User-Agent, which the GitHub API requires.
var client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}

// Options configures one self-update run.
type Options struct {
	// CurrentVersion is the running binary's version; "dev" or empty refuses without Force.
	CurrentVersion string
	// Target is the binary path to replace; it defaults to the running executable.
	Target string
	// Force updates a development build to the latest release.
	Force bool
	// Diagnostics receives progress output; nil disables it.
	Diagnostics io.Writer
}

// Result reports what one self-update run did.
type Result struct {
	// Updated reports whether the binary was replaced.
	Updated bool
	// Previous and Next name the versions around an update; Previous stays "" for dev builds.
	Previous string
	Next     string
}

// asset is one release download listed by the GitHub API.
type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// release is the part of the GitHub release payload self-update reads.
// The tags match GitHub's snake_case keys: without them encoding/json would not
// match "tag_name" or "browser_download_url" to these fields and they would
// decode empty.
type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

// archiveName returns the GoReleaser archive name for a platform, matching the
// name_template in .goreleaser.yaml, which titles the OS and renames x86_64 amd64.
func archiveName(goos, goarch string) (string, error) {
	switch goos {
	case "linux", "darwin":
	default:
		return "", fmt.Errorf("self-update has no release archive for %s/%s", goos, goarch)
	}
	var arch string
	switch goarch {
	case "amd64":
		arch = "x86_64"
	case "386":
		arch = "i386"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("self-update has no release archive for %s/%s", goos, goarch)
	}
	// GoReleaser's template titles the OS ({{ title .Os }}); goos is "linux"/"darwin", so a
	// first-letter capitalization is exact and avoids strings.Title, which is deprecated.
	return "shelf_" + strings.ToUpper(goos[:1]) + goos[1:] + "_" + arch + ".tar.gz", nil
}

// Update fetches the latest release and, when it differs from the current version,
// replaces the target binary after verifying its sha256 against checksums.txt.
func Update(ctx context.Context, options Options) (Result, error) {
	if options.Target == "" {
		executable, err := os.Executable()
		if err != nil {
			return Result{}, fmt.Errorf("locate the running binary: %w", err)
		}
		options.Target = executable
	}
	if !options.Force && (options.CurrentVersion == "" || options.CurrentVersion == "dev") {
		return Result{}, fmt.Errorf("self-update refused: this is a development build (%q); install a release or pass --force", displayVersion(options.CurrentVersion))
	}
	latest, err := fetchRelease(ctx)
	if err != nil {
		return Result{}, err
	}
	next := strings.TrimPrefix(latest.TagName, "v")
	if next != "" && next == strings.TrimPrefix(options.CurrentVersion, "v") && !options.Force {
		return Result{Updated: false, Next: next}, nil
	}
	name, err := archiveName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}
	url, ok := assetURL(latest, name)
	if !ok {
		return Result{}, fmt.Errorf("release %s has no %s asset", latest.TagName, name)
	}
	logf(options.Diagnostics, "Downloading %s", name)
	archive, err := download(ctx, url)
	if err != nil {
		return Result{}, err
	}
	logf(options.Diagnostics, "Verifying %s", name)
	checksums, ok := checksumsURL(latest)
	if !ok {
		return Result{}, fmt.Errorf("release %s has no checksums asset", latest.TagName)
	}
	if err := verifyChecksum(ctx, checksums, name, archive); err != nil {
		return Result{}, err
	}
	binary, err := extractBinary(archive)
	if err != nil {
		return Result{}, err
	}
	if err := installBinary(options.Target, binary); err != nil {
		return Result{}, err
	}
	return Result{Updated: true, Previous: options.CurrentVersion, Next: next}, nil
}

// displayVersion names an empty version for error messages.
func displayVersion(version string) string {
	if version == "" {
		return "dev"
	}
	return version
}

// fetchRelease returns the latest published release.
func fetchRelease(ctx context.Context) (release, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/repos/rubiin/shelf/releases/latest", nil)
	if err != nil {
		return release{}, err
	}
	request.Header.Set("User-Agent", "shelf-self-update")
	response, err := client.Do(request)
	if err != nil {
		return release{}, fmt.Errorf("query the latest release: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return release{}, fmt.Errorf("query the latest release: HTTP %s", response.Status)
	}
	var latest release
	if err := json.NewDecoder(response.Body).Decode(&latest); err != nil {
		return release{}, fmt.Errorf("decode the latest release: %w", err)
	}
	if latest.TagName == "" {
		return release{}, fmt.Errorf("the latest release has no tag")
	}
	return latest, nil
}

// assetURL returns the download URL of a named release asset.
func assetURL(latest release, name string) (string, bool) {
	for _, candidate := range latest.Assets {
		if candidate.Name == name && candidate.BrowserDownloadURL != "" {
			return candidate.BrowserDownloadURL, true
		}
	}
	return "", false
}

// checksumsURL returns the release's checksums asset. GoReleaser names the file
// "<project>_<version>_checksums.txt" by default, so the exact name is tried
// first and any asset ending in "checksums.txt" is accepted as a fallback.
func checksumsURL(latest release) (string, bool) {
	if url, ok := assetURL(latest, "checksums.txt"); ok {
		return url, true
	}
	for _, candidate := range latest.Assets {
		if strings.HasSuffix(candidate.Name, "checksums.txt") && candidate.BrowserDownloadURL != "" {
			return candidate.BrowserDownloadURL, true
		}
	}
	return "", false
}

// download fetches a URL and returns its body.
func download(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "shelf-self-update")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("download %s: HTTP %s", url, response.Status)
	}
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	return contents, nil
}

// verifyChecksum checks contents against the sha256sum-format line for name in checksums.txt.
func verifyChecksum(ctx context.Context, url, name string, contents []byte) error {
	listing, err := download(ctx, url)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(contents)
	actual := hex.EncodeToString(digest[:])
	for _, line := range strings.Split(string(listing), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		// sha256sum writes "<digest>  <name>" with a two-space separator; a line's last field is the file name.
		if len(fields) == 2 && fields[1] == name {
			if fields[0] == actual {
				return nil
			}
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, actual, fields[0])
		}
	}
	return fmt.Errorf("checksums.txt has no entry for %s", name)
}

// extractBinary unpacks the shelf binary from a tar.gz archive.
func extractBinary(archive []byte) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open the release archive: %w", err)
	}
	defer func() { _ = gzipReader.Close() }()
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("the release archive has no shelf binary")
		}
		if err != nil {
			return nil, fmt.Errorf("read the release archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		switch header.Name {
		case "shelf", "./shelf":
			contents, err := io.ReadAll(io.LimitReader(tarReader, 1<<30))
			if err != nil {
				return nil, fmt.Errorf("read the shelf binary: %w", err)
			}
			return contents, nil
		}
	}
}

// installBinary writes contents to a sibling temp file and renames it over target,
// so a crash or a failed write leaves the previous binary in place.
func installBinary(target string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(target), ".shelf-update-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o755); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, target)
}

// logf writes a progress line when diagnostics are enabled.
func logf(diagnostics io.Writer, format string, arguments ...any) {
	if diagnostics == nil {
		return
	}
	_, _ = fmt.Fprintf(diagnostics, format+"\n", arguments...)
}
