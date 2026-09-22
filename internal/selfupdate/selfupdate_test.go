package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	var downloads int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/repos/rubiin/shelf/releases/latest" {
			entries := make([]asset, 0, len(assets))
			for _, name := range slices.Sorted(maps.Keys(assets)) {
				entries = append(entries, asset{Name: name, BrowserDownloadURL: server.URL + "/assets/" + name})
			}
			writer.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(writer).Encode(release{TagName: tag, Assets: entries}); err != nil {
				t.Errorf("encode release: %v", err)
			}
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

func TestUpdateRefusesADevelopmentBuild(t *testing.T) {
	// Any request would mean the refusal failed to short-circuit before network use.
	server, _ := newReleaseServer(t, "v1.0.0", nil)
	pointAPIAt(t, server)
	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, current := range []string{"dev", ""} {
		if _, err := Update(context.Background(), Options{CurrentVersion: current, Target: target}); err == nil {
			t.Errorf("self-update with version %q succeeded", current)
		} else if !strings.Contains(err.Error(), "development") {
			t.Errorf("error = %v, want a development-build refusal", err)
		}
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "old" {
		t.Errorf("binary changed: %q, %v", contents, err)
	}
}

func TestUpdateForceUpdatesADevelopmentBuild(t *testing.T) {
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

	result, err := Update(context.Background(), Options{CurrentVersion: "dev", Target: target, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.Next != "2.0.0" {
		t.Fatalf("result = %+v, want an update to 2.0.0", result)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != "new" {
		t.Errorf("binary = %q, %v, want the released binary", contents, err)
	}
}

func TestUpdateInstallsTheLatestRelease(t *testing.T) {
	archive := buildArchive(t, "new")
	archiveName := archiveNameFor(t)
	server, _ := newReleaseServer(t, "v2.0.0", map[string]string{
		archiveName:     string(archive),
		"checksums.txt": checksumLine(t, archiveName, archive),
	})
	pointAPIAt(t, server)
	directory := t.TempDir()
	target := filepath.Join(directory, "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer

	result, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Diagnostics: &diagnostics})
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

	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target}); err == nil {
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
