package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// archiveNameFor returns the platform's release archive name, skipping the test when the platform has no release archive.
func archiveNameFor(t *testing.T) string {
	t.Helper()
	name, err := archiveName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("no release archive for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return name
}

// buildArchive returns a release tar.gz whose shelf binary holds the given contents.
func buildArchive(t *testing.T, contents string) []byte {
	t.Helper()
	var raw bytes.Buffer
	gzipWriter := gzip.NewWriter(&raw)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "shelf", Mode: 0o755, Size: int64(len(contents))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

// checksumLine returns the sha256sum-format line for the named contents.
func checksumLine(t *testing.T, name string, contents []byte) string {
	t.Helper()
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:]) + "  " + name
}

// newReleaseServer serves a fake GitHub API and asset host; assets maps asset names to contents.
// The returned counter tracks asset downloads, so tests can assert none happened.
func newReleaseServer(t *testing.T, tag string, assets map[string]string) (*httptest.Server, *int) {
	t.Helper()
	return newReleaseServerFor(t, []release{{TagName: tag}}, assets)
}

// newReleaseServerFor serves the given releases, newest first, so a test can
// pin one by tag while the newest is a different release.
func newReleaseServerFor(t *testing.T, releases []release, assets map[string]string) (*httptest.Server, *int) {
	t.Helper()
	var downloads int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/repos/rubiin/shelf/releases/latest":
			writeRelease(writer, t, withAssets(server, releases[0], assets))
			return
		case strings.HasPrefix(request.URL.Path, "/repos/rubiin/shelf/releases/tags/"):
			tag := strings.TrimPrefix(request.URL.Path, "/repos/rubiin/shelf/releases/tags/")
			for _, candidate := range releases {
				if candidate.TagName == tag {
					writeRelease(writer, t, withAssets(server, candidate, assets))
					return
				}
			}
			http.NotFound(writer, request)
			return
		}
		if name, ok := strings.CutPrefix(request.URL.Path, "/assets/"); ok {
			downloads++
			if contents, ok := assets[name]; ok {
				_, _ = writer.Write([]byte(contents))
				return
			}
			http.NotFound(writer, request)
			return
		}
		http.NotFound(writer, request)
	}))
	t.Cleanup(server.Close)
	return server, &downloads
}

// withAssets lists the served assets on a release, pointing each at the test server.
func withAssets(server *httptest.Server, candidate release, assets map[string]string) release {
	entries := make([]asset, 0, len(assets))
	for _, name := range slices.Sorted(maps.Keys(assets)) {
		entries = append(entries, asset{Name: name, BrowserDownloadURL: server.URL + "/assets/" + name})
	}
	candidate.Assets = entries
	return candidate
}

func writeRelease(writer http.ResponseWriter, t *testing.T, payload release) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		t.Errorf("encode release: %v", err)
	}
}

// pointAPIAt redirects the GitHub API to the server for the duration of the test.
func pointAPIAt(t *testing.T, server *httptest.Server) {
	t.Helper()
	original := apiBase
	apiBase = server.URL
	t.Cleanup(func() { apiBase = original })
}

func TestArchiveNameMatchesTheGoReleaserTemplate(t *testing.T) {
	tests := []struct {
		goos   string
		goarch string
		want   string
	}{
		{goos: "linux", goarch: "amd64", want: "shelf_Linux_x86_64.tar.gz"},
		{goos: "linux", goarch: "386", want: "shelf_Linux_i386.tar.gz"},
		{goos: "linux", goarch: "arm64", want: "shelf_Linux_arm64.tar.gz"},
		{goos: "darwin", goarch: "amd64", want: "shelf_Darwin_x86_64.tar.gz"},
		{goos: "darwin", goarch: "arm64", want: "shelf_Darwin_arm64.tar.gz"},
	}
	for _, test := range tests {
		name, err := archiveName(test.goos, test.goarch)
		if err != nil {
			t.Fatalf("archiveName(%s, %s): %v", test.goos, test.goarch, err)
		}
		if name != test.want {
			t.Errorf("archiveName(%s, %s) = %q, want %q", test.goos, test.goarch, name, test.want)
		}
	}
	for _, platform := range [][2]string{{"windows", "amd64"}, {"freebsd", "amd64"}, {"linux", "mips64"}} {
		if _, err := archiveName(platform[0], platform[1]); err == nil {
			t.Errorf("archiveName(%s, %s) succeeded for an unreleased platform", platform[0], platform[1])
		}
	}
}

func TestUpdateKeepsAnUpToDateBinary(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, downloads := newReleaseServer(t, "v1.2.3", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Update(context.Background(), Options{CurrentVersion: "1.2.3", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated {
		t.Fatalf("result = %+v, want an up-to-date short-circuit", result)
	}
	if result.Next != "1.2.3" {
		t.Errorf("result.Next = %q, want 1.2.3", result.Next)
	}
	if *downloads != 0 {
		t.Errorf("up-to-date run downloaded %d assets, want 0", *downloads)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "old" {
		t.Errorf("binary changed: %q, %v", contents, err)
	}
}

// A development build has no released version to compare against, so it
// updates like any other install.
func TestUpdateUpdatesADevelopmentBuild(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, current := range []string{"dev", ""} {
		result, err := Update(context.Background(), Options{CurrentVersion: current, Target: target, Yes: true})
		if err != nil {
			t.Fatalf("self-update with version %q: %v", current, err)
		}
		if !result.Updated || result.Next != "2.0.0" {
			t.Fatalf("result = %+v, want an update to 2.0.0", result)
		}
		if contents, err := os.ReadFile(target); err != nil || string(contents) != "new" {
			t.Fatalf("binary = %q, %v, want the released binary", contents, err)
		}
		if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpdateInstallsTheLatestRelease(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	// The checksums asset uses GoReleaser's default "<project>_<version>_checksums.txt" name.
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:                 string(archive),
		"shelf_2.0.0_checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	directory := t.TempDir()
	target := filepath.Join(directory, "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer

	result, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Diagnostics: &diagnostics, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.Previous != "1.0.0" || result.Next != "2.0.0" {
		t.Fatalf("result = %+v, want an update from 1.0.0 to 2.0.0", result)
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "new" {
		t.Fatalf("binary = %q, %v, want the released binary", contents, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("binary mode = %v, want 0755", info.Mode().Perm())
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".shelf-update-") {
			t.Errorf("temporary file %s survived the update", entry.Name())
		}
	}
	if !strings.Contains(diagnostics.String(), "Downloading "+archiveName) {
		t.Errorf("diagnostics = %q, want a download status", diagnostics.String())
	}
}

// An animated phase replaces its log line, so Progress reports each phase and
// Diagnostics stays free of them.
func TestUpdateReportsPhasesToProgress(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	var phases []string

	result, err := Update(context.Background(), Options{
		CurrentVersion: "1.0.0",
		Target:         target,
		Diagnostics:    &diagnostics,
		Progress:       func(message string) { phases = append(phases, message) },
		Yes:            true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated {
		t.Fatal("self-update reported no update")
	}
	want := []string{"Downloading " + archiveName, "Verifying " + archiveName}
	if !slices.Equal(phases, want) {
		t.Errorf("phases = %q, want %q", phases, want)
	}
	if strings.Contains(diagnostics.String(), "Downloading") || strings.Contains(diagnostics.String(), "Verifying") {
		t.Errorf("diagnostics = %q, want the phases animated rather than logged", diagnostics.String())
	}
}

func TestUpdateRefusesAnUnwritableTargetDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, downloads := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	directory := t.TempDir()
	target := filepath.Join(directory, "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })

	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target}); err == nil {
		t.Fatal("self-update succeeded in an unwritable directory")
	} else if !strings.Contains(err.Error(), "not writable") {
		t.Errorf("error = %v, want a not-writable refusal", err)
	}
	if *downloads != 0 {
		t.Errorf("refusal downloaded %d assets, want 0", *downloads)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "old" {
		t.Errorf("binary changed: %q, %v", contents, err)
	}
}

func TestUpdateRejectsAChecksumMismatch(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, []byte("not the archive")),
	})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Yes: true}); err == nil {
		t.Fatal("self-update accepted a checksum mismatch")
	} else if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error = %v, want a checksum mismatch", err)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "old" {
		t.Errorf("binary changed: %q, %v", contents, err)
	}
}

func TestUpdateRejectsAMissingArchiveAsset(t *testing.T) {
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{"checksums.txt": ""})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	archiveName := archiveNameFor(t)
	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target}); err == nil {
		t.Fatal("self-update succeeded without an archive asset")
	} else if !strings.Contains(err.Error(), archiveName) {
		t.Errorf("error = %v, want a mention of %s", err, archiveName)
	}
}

func TestChecksumsURLAcceptsGoReleaserNaming(t *testing.T) {
	tests := []struct {
		name   string
		assets []asset
		want   string
		ok     bool
	}{
		{name: "exact name", assets: []asset{{Name: "checksums.txt", BrowserDownloadURL: "a"}}, want: "a", ok: true},
		{name: "versioned name", assets: []asset{{Name: "shelf_0.4.1_checksums.txt", BrowserDownloadURL: "b"}}, want: "b", ok: true},
		{name: "missing", assets: []asset{{Name: "shelf_Linux_x86_64.tar.gz", BrowserDownloadURL: "c"}}, ok: false},
		{name: "no download URL", assets: []asset{{Name: "checksums.txt"}}, ok: false},
	}
	for _, test := range tests {
		got, ok := checksumsURL(release{Assets: test.assets})
		if ok != test.ok || got != test.want {
			t.Errorf("%s: checksumsURL = %q, %v, want %q, %v", test.name, got, ok, test.want, test.ok)
		}
	}
}

// TestFetchReleaseDecodesGitHubJSONKeys guards the snake_case keys GitHub really
// sends: encoding/json only matches underscored keys to fields via explicit tags.
func TestFetchReleaseDecodesGitHubJSONKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"tag_name":"v1.2.3","assets":[{"name":"shelf_Linux_x86_64.tar.gz","browser_download_url":"https://example.com/shelf_Linux_x86_64.tar.gz"}]}`))
	}))
	t.Cleanup(server.Close)
	pointAPIAt(t, server)

	latest, err := fetchRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latest.TagName != "v1.2.3" {
		t.Errorf("TagName = %q, want v1.2.3", latest.TagName)
	}
	if url, ok := assetURL(latest, "shelf_Linux_x86_64.tar.gz"); !ok || url != "https://example.com/shelf_Linux_x86_64.tar.gz" {
		t.Errorf("assetURL = %q, %v, want the browser download URL", url, ok)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{name: "equal", a: "1.2.3", b: "1.2.3", want: 0},
		{name: "v prefixes equal", a: "v1.2.3", b: "1.2.3", want: 0},
		{name: "numeric not lexical", a: "1.10.0", b: "1.9.9", want: 1},
		{name: "patch level", a: "1.2.3", b: "1.2.4", want: -1},
		{name: "build metadata ignored", a: "1.2.3+build", b: "1.2.3", want: 0},
		{name: "build metadata on the other side", a: "1.2.3", b: "1.2.3+linux.amd64", want: 0},
		{name: "prerelease below release", a: "1.0.0-alpha", b: "1.0.0", want: -1},
		{name: "release above prerelease", a: "1.0.0", b: "1.0.0-alpha", want: 1},
		{name: "alphanumeric identifier order", a: "1.0.0-alpha", b: "1.0.0-beta", want: -1},
		{name: "numeric identifier before alphanumeric", a: "1.0.0-alpha.1", b: "1.0.0-alpha.beta", want: -1},
		{name: "shorter prerelease prefix", a: "1.0.0-alpha", b: "1.0.0-alpha.1", want: -1},
		{name: "numeric identifiers by value", a: "1.0.0-beta.2", b: "1.0.0-beta.11", want: -1},
		{name: "longer prerelease wins", a: "1.0.0-beta.11", b: "1.0.0-beta.2", want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left, leftOK := parseVersion(test.a)
			right, rightOK := parseVersion(test.b)
			if !leftOK || !rightOK {
				t.Fatalf("parseVersion(%q, %q) = (%v, %v), both must parse", test.a, test.b, leftOK, rightOK)
			}
			got := sign(compareVersions(left, right))
			if got != test.want {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", test.a, test.b, got, test.want)
			}
			if reversed := sign(compareVersions(right, left)); reversed != -test.want {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", test.b, test.a, reversed, -test.want)
			}
		})
	}
}

func TestUpdateDoesNotDowngradeANewerBinary(t *testing.T) {
	// A 2.0.0 binary must skip a v1.5.0 release: equality-only matching would
	// redownload and replace it with an older binary.
	server, downloads := newReleaseServer(t, "v1.5.0", nil)
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Update(context.Background(), Options{CurrentVersion: "2.0.0", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated {
		t.Fatalf("result = %+v, want a downgrade refusal", result)
	}
	if result.Next != "1.5.0" {
		t.Errorf("result.Next = %q, want 1.5.0", result.Next)
	}
	if *downloads != 0 {
		t.Errorf("downgrade run downloaded %d assets, want 0", *downloads)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "current" {
		t.Errorf("binary changed: %q, %v", contents, err)
	}
}

func TestUpdatePinsAnExplicitVersion(t *testing.T) {
	// An explicit version may move backwards on purpose, unlike an unpinned run.
	archive := buildArchive(t, "pinned")
	archiveName := archiveNameFor(t)
	assets := map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	}
	server, _ := newReleaseServerFor(t, []release{{TagName: "v2.0.0"}, {TagName: "v1.0.0"}}, assets)
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Update(context.Background(), Options{CurrentVersion: "2.0.0", Target: target, Version: "1.0.0", Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.Next != "1.0.0" {
		t.Fatalf("result = %+v, want the pinned downgrade to 1.0.0", result)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "pinned" {
		t.Errorf("binary = %q, %v, want the pinned binary", contents, err)
	}
}

func TestUpdateReportsAMissingPinnedRelease(t *testing.T) {
	server, downloads := newReleaseServerFor(t, []release{{TagName: "v2.0.0"}}, nil)
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Update(context.Background(), Options{CurrentVersion: "2.0.0", Target: target, Version: "9.9.9", Yes: true}); err == nil {
		t.Fatal("self-update succeeded for a release tag that does not exist")
	} else if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("error = %v, want an HTTP 404 for the missing tag", err)
	}
	if *downloads != 0 {
		t.Errorf("missing tag downloaded %d assets, want 0", *downloads)
	}
}

func TestUpdateSkipsAPinnedVersionAlreadyInstalled(t *testing.T) {
	server, downloads := newReleaseServer(t, "v1.2.3", nil)
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Update(context.Background(), Options{CurrentVersion: "1.2.3", Target: target, Version: "1.2.3", Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated || *downloads != 0 {
		t.Fatalf("result = %+v with %d downloads, want an up-to-date short-circuit", result, *downloads)
	}
}

func TestUpdateRequiresConfirmationBeforeReplacing(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, downloads := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	asked := ""
	_, err := Update(context.Background(), Options{
		CurrentVersion: "1.0.0",
		Target:         target,
		Confirm:        func(version string) (bool, error) { asked = version; return false, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want a cancellation pointing at --yes", err)
	}
	if asked != "2.0.0" {
		t.Errorf("confirmed version = %q, want 2.0.0", asked)
	}
	if *downloads != 0 {
		t.Errorf("declined run downloaded %d assets, want 0", *downloads)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "old" {
		t.Errorf("binary changed: %q, %v", contents, err)
	}
}

func TestUpdateRefusesWithoutAConfirmationHookOrYes(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, downloads := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want the --yes hint", err)
	}
	if *downloads != 0 {
		t.Errorf("unconfirmed run downloaded %d assets, want 0", *downloads)
	}
}

func TestUpdatePropagatesAConfirmationError(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, downloads := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Update(context.Background(), Options{
		CurrentVersion: "1.0.0",
		Target:         target,
		Confirm:        func(string) (bool, error) { return false, errors.New("prompt exploded") },
	})
	if err == nil || !strings.Contains(err.Error(), "prompt exploded") {
		t.Fatalf("err = %v, want the confirmation error", err)
	}
	if *downloads != 0 {
		t.Errorf("failed confirmation downloaded %d assets, want 0", *downloads)
	}
}

func TestUpdateYesSkipsTheConfirmation(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.Next != "2.0.0" {
		t.Fatalf("result = %+v, want an update to 2.0.0", result)
	}
}

func TestStickyBlocksRename(t *testing.T) {
	tests := []struct {
		name          string
		directoryMode os.FileMode
		directoryUID  uint32
		fileUID       uint32
		euid          uint32
		want          bool
	}{
		{name: "non-sticky directory", directoryMode: 0o777, directoryUID: 0, fileUID: 0, euid: 1000},
		{name: "sticky, root replaces anything", directoryMode: os.ModeSticky | 0o777, directoryUID: 0, fileUID: 0, euid: 0},
		{name: "sticky, directory owned by user", directoryMode: os.ModeSticky | 0o777, directoryUID: 1000, fileUID: 0, euid: 1000},
		{name: "sticky, file owned by user", directoryMode: os.ModeSticky | 0o777, directoryUID: 0, fileUID: 1000, euid: 1000},
		{name: "sticky foreign binary", directoryMode: os.ModeSticky | 0o777, directoryUID: 0, fileUID: 0, euid: 1000, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := stickyBlocksRename(test.directoryMode, test.directoryUID, test.fileUID, test.euid); got != test.want {
				t.Errorf("stickyBlocksRename(%v, %d, %d, %d) = %t, want %t", test.directoryMode, test.directoryUID, test.fileUID, test.euid, got, test.want)
			}
		})
	}
}

func TestSelfUpdateAvailable(t *testing.T) {
	tests := []struct {
		name      string
		relative  string
		envKey    string
		envValue  string
		available bool
	}{
		{name: "plain install", available: true},
		{name: "lib marker", relative: "lib/.disable-self-update"},
		{name: "lib shelf marker", relative: "lib/shelf/.disable-self-update"},
		{name: "lib64 marker", relative: "lib64/shelf/.disable-self-update"},
		{name: "instructions file", relative: "lib/shelf/shelf-self-update-instructions.toml"},
		{name: "env re-enables a disabled install", relative: "lib/.disable-self-update", envKey: "SHELF_SELF_UPDATE_AVAILABLE", envValue: "true", available: true},
		{name: "env disables a plain install", envKey: "SHELF_SELF_UPDATE_AVAILABLE", envValue: "false"},
		{name: "instructions env disables", envKey: "SHELF_SELF_UPDATE_INSTRUCTIONS", envValue: "missing.toml"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SHELF_SELF_UPDATE_AVAILABLE", "")
			t.Setenv("SHELF_SELF_UPDATE_INSTRUCTIONS", "")
			prefix := t.TempDir()
			if test.relative != "" {
				path := filepath.Join(prefix, test.relative)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("marker"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if test.envKey != "" {
				t.Setenv(test.envKey, test.envValue)
			}
			if got := selfUpdateAvailable(prefix); got != test.available {
				t.Errorf("selfUpdateAvailable = %t, want %t", got, test.available)
			}
		})
	}
}

func TestInstallPrefix(t *testing.T) {
	if got := installPrefix("/usr/bin/shelf"); got != "/usr" {
		t.Errorf("installPrefix(/usr/bin/shelf) = %q, want /usr", got)
	}
}

func TestInstructionsMessage(t *testing.T) {
	directory := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	if got := instructionsMessage(write("message.toml", "message = \"run apt upgrade shelf\"\n")); got != "run apt upgrade shelf" {
		t.Errorf("message = %q, want the message key", got)
	}
	// Named commands are flattened, so the lexicographically first one is used.
	if got := instructionsMessage(write("commands.toml", "zypper = \"zypper update shelf\"\napt = \"apt upgrade shelf\"\n")); got != "apt upgrade shelf" {
		t.Errorf("commands = %q, want the first command", got)
	}
	if got := instructionsMessage(write("invalid.toml", "not = [unclosed\n")); got != "" {
		t.Errorf("invalid TOML = %q, want empty", got)
	}
	if got := instructionsMessage(write("scalar.toml", "version = 3\n")); got != "" {
		t.Errorf("non-string values = %q, want empty", got)
	}
	if got := instructionsMessage(filepath.Join(directory, "missing.toml")); got != "" {
		t.Errorf("missing file = %q, want empty", got)
	}
}

// packageManagedTarget lays out a fake install prefix with the binary at
// <prefix>/bin/shelf, so the marker and instructions paths sit under it.
func packageManagedTarget(t *testing.T) (prefix, target string) {
	t.Helper()
	prefix = t.TempDir()
	target = filepath.Join(prefix, "bin", "shelf")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return prefix, target
}

// writePackagerFile creates a file under the install prefix.
func writePackagerFile(t *testing.T, prefix, relative, body string) {
	t.Helper()
	path := filepath.Join(prefix, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRefusesAPackageManagedInstall(t *testing.T) {
	t.Setenv("SHELF_SELF_UPDATE_AVAILABLE", "")
	t.Setenv("SHELF_SELF_UPDATE_INSTRUCTIONS", "")
	prefix, target := packageManagedTarget(t)
	writePackagerFile(t, prefix, "lib/shelf/.disable-self-update", "")
	server, downloads := newReleaseServer(t, "v2.0.0", nil)
	pointAPIAt(t, server)
	var diagnostics bytes.Buffer

	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Yes: true, Diagnostics: &diagnostics}); err == nil {
		t.Fatal("self-update replaced a package-managed install")
	} else if !strings.Contains(err.Error(), "installed via a package manager") {
		t.Errorf("error = %v, want a package-manager refusal", err)
	}
	if !strings.Contains(diagnostics.String(), "self-update is disabled for this install") {
		t.Errorf("diagnostics = %q, want the disabled self-update warning", diagnostics.String())
	}
	if *downloads != 0 {
		t.Errorf("refusal downloaded %d assets, want 0", *downloads)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "old" {
		t.Errorf("binary changed: %q, %v", contents, err)
	}
}

func TestUpdateForceOverridesAPackageManagedInstall(t *testing.T) {
	t.Setenv("SHELF_SELF_UPDATE_AVAILABLE", "")
	t.Setenv("SHELF_SELF_UPDATE_INSTRUCTIONS", "")
	prefix, target := packageManagedTarget(t)
	writePackagerFile(t, prefix, "lib/shelf/.disable-self-update", "")
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)

	result, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Force: true, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated {
		t.Fatalf("result = %+v, want --force to update a package-managed install", result)
	}
}

func TestUpdateHonoursTheSelfUpdateAvailableOverride(t *testing.T) {
	prefix, target := packageManagedTarget(t)
	writePackagerFile(t, prefix, "lib/.disable-self-update", "")
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	t.Setenv("SHELF_SELF_UPDATE_INSTRUCTIONS", "")
	t.Setenv("SHELF_SELF_UPDATE_AVAILABLE", "true")

	result, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated {
		t.Fatalf("result = %+v, want the override to allow the update", result)
	}
}

func TestUpdatePrintsPackagerInstructions(t *testing.T) {
	t.Setenv("SHELF_SELF_UPDATE_AVAILABLE", "")
	t.Setenv("SHELF_SELF_UPDATE_INSTRUCTIONS", "")
	prefix, target := packageManagedTarget(t)
	writePackagerFile(t, prefix, "lib/shelf/shelf-self-update-instructions.toml", "message = \"run apt upgrade shelf\"\n")
	server, _ := newReleaseServer(t, "v2.0.0", nil)
	pointAPIAt(t, server)
	var diagnostics bytes.Buffer

	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Yes: true, Diagnostics: &diagnostics}); err == nil {
		t.Fatal("self-update replaced an install that ships instructions")
	} else if !strings.Contains(err.Error(), "installed via a package manager") {
		t.Errorf("error = %v, want a package-manager refusal", err)
	}
	if !strings.Contains(diagnostics.String(), "apt upgrade shelf") {
		t.Errorf("diagnostics = %q, want the packager instructions", diagnostics.String())
	}
}

func TestTagForNormalizesTheReleaseTag(t *testing.T) {
	for input, want := range map[string]string{"1.2.3": "v1.2.3", "v1.2.3": "v1.2.3"} {
		if got := tagFor(input); got != want {
			t.Errorf("tagFor(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestUpdateTreatsVPrefixesTheSame(t *testing.T) {
	// A release tagged "1.2.3" and an installed "v1.2.3" name the same version.
	for _, test := range []struct {
		name    string
		current string
		tag     string
	}{
		{name: "installed without v", current: "1.2.3", tag: "v1.2.3"},
		{name: "installed with v", current: "v1.2.3", tag: "1.2.3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, downloads := newReleaseServer(t, test.tag, nil)
			pointAPIAt(t, server)
			target := filepath.Join(t.TempDir(), "shelf")
			if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}

			result, err := Update(context.Background(), Options{CurrentVersion: test.current, Target: target})
			if err != nil {
				t.Fatal(err)
			}
			if result.Updated {
				t.Fatalf("result = %+v, want an up-to-date short-circuit", result)
			}
			if *downloads != 0 {
				t.Errorf("run downloaded %d assets, want 0", *downloads)
			}
		})
	}
}

func TestUpdatePreservesASymlinkedTarget(t *testing.T) {
	// Package managers install shelf through a symlink; an update must write
	// through the link to the real binary, not replace the link itself.
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	directory := t.TempDir()
	real := filepath.Join(directory, "shelf.real")
	if err := os.WriteFile(real, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "shelf")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	result, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: link, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated {
		t.Fatalf("result = %+v, want an update through the symlink", result)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("update replaced the symlink with a regular file")
	}
	if resolved, err := os.Readlink(link); err != nil || resolved != real {
		t.Errorf("symlink now points at %q, %v, want %q", resolved, err, real)
	}
	if contents, err := os.ReadFile(real); err != nil || string(contents) != "new" {
		t.Errorf("real binary = %q, %v, want the released binary", contents, err)
	}
}

// TestUpdateFailsWithoutAnExplicitTarget requires a run with no target to fail
// while resolving the running binary, before any release lookup.
func TestUpdateFailsWithoutAnExplicitTarget(t *testing.T) {
	if _, err := Update(context.Background(), Options{CurrentVersion: "dev"}); err == nil {
		t.Fatal("self-update with no target succeeded")
	}
}

// TestUpdatePropagatesATargetResolutionError covers a target path that cannot
// even be resolved (its parent is a regular file) rather than a missing file.
func TestUpdatePropagatesATargetResolutionError(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: filepath.Join(file, "shelf")}); err == nil {
		t.Fatal("self-update resolved a target through a regular file")
	} else if !strings.Contains(err.Error(), "resolve the running binary") {
		t.Errorf("error = %v, want a target-resolution error", err)
	}
}

func TestUpdatePropagatesAFetchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.NotFound(writer, nil)
	}))
	t.Cleanup(server.Close)
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target}); err == nil {
		t.Fatal("self-update succeeded when fetching the release failed")
	} else if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("error = %v, want an HTTP 404 fetch error", err)
	}
}

func TestUpdateReportsAMissingChecksumsAsset(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{archiveName: string(archive)})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Yes: true}); err == nil {
		t.Fatal("self-update succeeded without a checksums asset")
	} else if !strings.Contains(err.Error(), "checksums") {
		t.Errorf("error = %v, want a missing-checksums error", err)
	}
}

func TestUpdateRejectsAnUnreadableArchive(t *testing.T) {
	archiveName := archiveNameFor(t)
	junk := []byte("not a gzip archive")
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(junk),
		"checksums.txt": checksumLine(t, archiveName, junk),
	})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Yes: true}); err == nil {
		t.Fatal("self-update accepted an unreadable archive")
	} else if !strings.Contains(err.Error(), "archive") {
		t.Errorf("error = %v, want an archive error", err)
	}
}

func TestUpdateRefusesToReplaceADirectory(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Yes: true}); err == nil {
		t.Fatal("self-update replaced an existing directory")
	} else if !strings.Contains(err.Error(), "replace") {
		t.Errorf("error = %v, want a replace error", err)
	}
}

func TestFetchReleaseErrors(t *testing.T) {
	t.Run("request creation", func(t *testing.T) {
		original := apiBase
		apiBase = "://"
		t.Cleanup(func() { apiBase = original })
		if _, err := fetchRelease(context.Background()); err == nil {
			t.Fatal("fetchRelease accepted an unparseable API base")
		}
	})
	t.Run("transport", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		server.Close()
		pointAPIAt(t, server)
		if _, err := fetchRelease(context.Background()); err == nil {
			t.Fatal("fetchRelease succeeded against a closed server")
		}
	})
	t.Run("non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(server.Close)
		pointAPIAt(t, server)
		if _, err := fetchRelease(context.Background()); err == nil {
			t.Fatal("fetchRelease accepted an HTTP 500")
		}
	})
	t.Run("invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte("not json"))
		}))
		t.Cleanup(server.Close)
		pointAPIAt(t, server)
		if _, err := fetchRelease(context.Background()); err == nil {
			t.Fatal("fetchRelease accepted invalid JSON")
		}
	})
	t.Run("missing tag", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(`{}`))
		}))
		t.Cleanup(server.Close)
		pointAPIAt(t, server)
		if _, err := fetchRelease(context.Background()); err == nil {
			t.Fatal("fetchRelease accepted a release without a tag")
		}
	})
}

func TestDownloadErrors(t *testing.T) {
	t.Run("unparseable URL", func(t *testing.T) {
		if _, err := download(context.Background(), "://"); err == nil {
			t.Fatal("download accepted an unparseable URL")
		}
	})
	t.Run("transport", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		server.Close()
		if _, err := download(context.Background(), server.URL); err == nil {
			t.Fatal("download succeeded against a closed server")
		}
	})
	t.Run("non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNotFound)
		}))
		t.Cleanup(server.Close)
		if _, err := download(context.Background(), server.URL); err == nil {
			t.Fatal("download accepted an HTTP 404")
		}
	})
}

func TestVerifyChecksumErrors(t *testing.T) {
	t.Run("listing download fails", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		server.Close()
		if err := verifyChecksum(context.Background(), server.URL, "shelf", []byte("x")); err == nil {
			t.Fatal("verifyChecksum succeeded when the listing download failed")
		}
	})
	t.Run("no entry for the archive", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte("deadbeef  some-other-name\n"))
		}))
		t.Cleanup(server.Close)
		if err := verifyChecksum(context.Background(), server.URL, "shelf", []byte("x")); err == nil {
			t.Fatal("verifyChecksum accepted a listing without the archive name")
		} else if !strings.Contains(err.Error(), "no entry") {
			t.Errorf("error = %v, want a no-entry error", err)
		}
	})
}

// buildEmptyArchive returns a gzip tar with no entries.
func buildEmptyArchive(t *testing.T) []byte {
	t.Helper()
	var raw bytes.Buffer
	gzipWriter := gzip.NewWriter(&raw)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

// buildArchiveWithDirectory returns an archive with a directory entry before the shelf binary.
func buildArchiveWithDirectory(t *testing.T, contents string) []byte {
	t.Helper()
	var raw bytes.Buffer
	gzipWriter := gzip.NewWriter(&raw)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "lib", Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.WriteHeader(&tar.Header{Name: "shelf", Mode: 0o755, Size: int64(len(contents))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func TestExtractBinaryErrors(t *testing.T) {
	t.Run("not gzip", func(t *testing.T) {
		if _, err := extractBinary([]byte("junk")); err == nil {
			t.Fatal("extractBinary accepted a non-gzip archive")
		}
	})
	t.Run("no shelf binary", func(t *testing.T) {
		if _, err := extractBinary(buildEmptyArchive(t)); err == nil {
			t.Fatal("extractBinary accepted an archive without a shelf binary")
		}
	})
	t.Run("truncated archive", func(t *testing.T) {
		archive := buildArchive(t, "new")
		if _, err := extractBinary(archive[:20]); err == nil {
			t.Fatal("extractBinary accepted a truncated archive")
		}
	})
	t.Run("skips non-file entries", func(t *testing.T) {
		contents, err := extractBinary(buildArchiveWithDirectory(t, "bin"))
		if err != nil {
			t.Fatal(err)
		}
		if string(contents) != "bin" {
			t.Errorf("extracted binary = %q, want %q", contents, "bin")
		}
	})
}

func TestResolveTarget(t *testing.T) {
	t.Run("missing target", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "absent")
		if resolved, err := resolveTarget(target); err != nil || resolved != target {
			t.Errorf("resolveTarget(%q) = %q, %v, want the target unchanged", target, resolved, err)
		}
	})
	t.Run("relative symlink", func(t *testing.T) {
		directory := t.TempDir()
		real := filepath.Join(directory, "shelf.real")
		if err := os.WriteFile(real, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "shelf")
		if err := os.Symlink("shelf.real", link); err != nil {
			t.Fatal(err)
		}
		resolved, err := resolveTarget(link)
		if err != nil {
			t.Fatal(err)
		}
		if resolved != real {
			t.Errorf("resolveTarget(%q) = %q, want %q", link, resolved, real)
		}
	})
	t.Run("path through a file", func(t *testing.T) {
		directory := t.TempDir()
		file := filepath.Join(directory, "not-a-dir")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveTarget(filepath.Join(file, "shelf")); err == nil {
			t.Fatal("resolveTarget accepted a path through a regular file")
		}
	})
}

func TestInstallBinaryErrors(t *testing.T) {
	t.Run("unwritable directory", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "absent", "shelf")
		if err := installBinary(target, []byte("x")); err == nil {
			t.Fatal("installBinary succeeded in a missing directory")
		}
	})
	t.Run("target is a directory", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "shelf")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := installBinary(target, []byte("x")); err == nil {
			t.Fatal("installBinary replaced an existing directory")
		}
	})
}

func TestAlreadyCurrentExactFallback(t *testing.T) {
	tests := []struct {
		name    string
		current string
		tag     string
		want    bool
	}{
		{"identical non-semver tags", "nightly", "nightly", true},
		{"different non-semver tags", "nightly", "beta", false},
		{"v-prefix counts as equal", "vnightly", "nightly", true},
		{"semver vs non-semver", "1.2.3", "nightly", false},
	}
	for _, test := range tests {
		if got := alreadyCurrent(test.current, test.tag); got != test.want {
			t.Errorf("alreadyCurrent(%q, %q) = %v, want %v", test.current, test.tag, got, test.want)
		}
	}
}

func TestParseVersionRejectsMalformed(t *testing.T) {
	for _, input := range []string{"1.2", "1.2.3.4", "1..3", "1.2.x", "-1.2.3", "abc", "v", "1.2.-3"} {
		if _, ok := parseVersion(input); ok {
			t.Errorf("parseVersion(%q) parsed, want rejection", input)
		}
	}
}

// TestUpdatePropagatesADownloadError pins the fetch-succeeds/download-fails
// leg of Update: release metadata is valid but the archive's endpoint is dead.
func TestUpdatePropagatesADownloadError(t *testing.T) {
	archiveName := archiveNameFor(t)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	payload, err := json.Marshal(release{
		TagName: "v2.0.0",
		Assets:  []asset{{Name: archiveName, BrowserDownloadURL: deadURL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/releases/latest") {
			_, _ = writer.Write(payload)
			return
		}
		http.NotFound(writer, request)
	}))
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Yes: true})
	if err == nil || !strings.Contains(err.Error(), "download") {
		t.Fatalf("err = %v, want a download error", err)
	}
}

// TestDownloadSurfacesABodyReadError covers a body that ends before its
// declared Content-Length: the read must fail instead of returning a short
// archive as if it were whole.
func TestDownloadSurfacesABodyReadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", "100")
		_, _ = writer.Write([]byte("short"))
	}))
	t.Cleanup(server.Close)
	if _, err := download(context.Background(), server.URL); err == nil {
		t.Fatal("download accepted a truncated body")
	}
}

// buildArchiveWithShortEntry returns a tar.gz whose shelf entry declares 100
// bytes but carries only 3, so reading it fails mid-entry.
func buildArchiveWithShortEntry(t *testing.T) []byte {
	t.Helper()
	var raw bytes.Buffer
	gzipWriter := gzip.NewWriter(&raw)

	var header [512]byte
	copy(header[0:], []byte("shelf"))
	copy(header[100:], []byte("0000755\x00"))     // mode
	copy(header[108:], []byte("0000000\x00"))     // uid
	copy(header[116:], []byte("0000000\x00"))     // gid
	copy(header[124:], []byte("00000000100\x00")) // size (octal 100 = 64 bytes)
	copy(header[136:], []byte("00000000000\x00")) // mtime
	header[156] = '0'                             // regular file
	copy(header[257:], []byte("ustar\x00\x00"))   // magic and version
	for index := 148; index < 156; index++ {
		header[index] = ' '
	}
	checksum := 0
	for _, b := range header {
		checksum += int(b)
	}
	copy(header[148:], []byte(fmt.Sprintf("%06o", checksum)))
	header[154] = 0
	if _, err := gzipWriter.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := gzipWriter.Write([]byte("bin")); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func TestExtractBinarySurfacesAReadError(t *testing.T) {
	if _, err := extractBinary(buildArchiveWithShortEntry(t)); err == nil ||
		!strings.Contains(err.Error(), "read the shelf binary") {
		t.Fatalf("err = %v, want a shelf-binary read error", err)
	}
}

// TestInstallBinarySurfacesAWriteError covers a staging file that cannot take
// the new binary; the write must be reported instead of silently renamed over
// the target.
func TestInstallBinarySurfacesAWriteError(t *testing.T) {
	original := createTemp
	createTemp = func(directory, pattern string) (*os.File, error) {
		file, err := os.CreateTemp(directory, pattern)
		if err != nil {
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		return file, nil
	}
	t.Cleanup(func() { createTemp = original })
	if err := installBinary(filepath.Join(t.TempDir(), "shelf"), []byte("new")); err == nil {
		t.Fatal("installBinary accepted a write failure")
	}
}
