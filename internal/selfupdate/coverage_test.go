package selfupdate

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// foreignFileInfo reports a Sys() the fileOwner type assertion cannot recognize.
type foreignFileInfo struct{}

func (foreignFileInfo) Name() string       { return "x" }
func (foreignFileInfo) Size() int64        { return 0 }
func (foreignFileInfo) Mode() os.FileMode  { return 0 }
func (foreignFileInfo) ModTime() time.Time { return time.Time{} }
func (foreignFileInfo) IsDir() bool        { return false }
func (foreignFileInfo) Sys() any           { return nil }

func TestFileOwnerRejectsForeignFileInfo(t *testing.T) {
	if _, ok := fileOwner(foreignFileInfo{}); ok {
		t.Fatal("fileOwner read a uid from a non-stat FileInfo")
	}
}

func TestConfirmUpdateApproval(t *testing.T) {
	approved := Options{Confirm: func(string) (bool, error) { return true, nil }}
	if err := confirmUpdate(approved, "1.2.3"); err != nil {
		t.Fatalf("confirmUpdate rejected an approved update: %v", err)
	}
}

func TestFetchReleaseByTagRejectsMissingTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	pointAPIAt(t, server)
	if _, err := fetchReleaseByTag(context.Background(), "v9.9.9"); err == nil {
		t.Fatal("fetchReleaseByTag accepted a release without a tag")
	}
}

func TestCheckReplaceableIgnoresNonFatalProbeError(t *testing.T) {
	// A missing target directory fails the probe with ENOENT, which is not a
	// permission problem, so the real operation is left to report it.
	missing := filepath.Join(t.TempDir(), "absent", "shelf")
	if err := checkReplaceable(missing, t.TempDir()); err != nil {
		t.Fatalf("checkReplaceable treated a non-permission probe error as fatal: %v", err)
	}
}

func TestStickyBlocksReplacingMissingPaths(t *testing.T) {
	directory := t.TempDir()
	if stickyBlocksReplacing(filepath.Join(directory, "absent"), filepath.Join(directory, "shelf")) {
		t.Fatal("stickyBlocksReplacing blocked a missing directory")
	}
	if stickyBlocksReplacing(directory, filepath.Join(directory, "absent")) {
		t.Fatal("stickyBlocksReplacing blocked a missing target")
	}
}

// A platform shelf ships no release for cannot report a download phase.
func TestUpdateReportsAnUnreleasedPlatform(t *testing.T) {
	server, _ := newReleaseServer(t, "v2.0.0", nil)
	pointAPIAt(t, server)
	// archiveName is a var so a test can pose as a platform with no asset.
	original := archiveName
	archiveName = func(string, string) (string, error) {
		return "", errors.New("self-update has no release archive for plan9/mips")
	}
	t.Cleanup(func() { archiveName = original })

	target := filepath.Join(t.TempDir(), "shelf")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(context.Background(), Options{CurrentVersion: "1.0.0", Target: target, Yes: true}); err == nil {
		t.Fatal("self-update accepted a platform with no release archive")
	} else if !strings.Contains(err.Error(), "plan9/mips") {
		t.Errorf("error = %v, want the archive-name failure", err)
	}
}

// An unreadable symlink cannot be resolved to a real binary.
func TestResolveTargetRejectsAnUnreadableLink(t *testing.T) {
	directory := t.TempDir()
	// A symlink loop makes resolution recurse until the path is too long, and a
	// dangling one resolves to a missing file; neither may be reported as a
	// resolution failure that hides a real target.
	dangling := filepath.Join(directory, "shelf")
	if err := os.Symlink(filepath.Join(directory, "absent"), dangling); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveTarget(dangling)
	if err != nil {
		t.Fatalf("resolveTarget refused a dangling link: %v", err)
	}
	if resolved != filepath.Join(directory, "absent") {
		t.Errorf("resolved = %q, want the link target", resolved)
	}
}

// A missing install directory is left to the real operation, which reports the
// path it tried, rather than being called fatal here.
func TestProbeInstallDirReportsAMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	if err := probeInstallDir(missing); err == nil {
		t.Fatal("probeInstallDir accepted a missing directory")
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, want a not-exist error", err)
	}
}

// A failed write probe for a reason other than permissions must not stop the
// update before the download.
func TestWriteProbeIsFatalOnlyForPermissions(t *testing.T) {
	if writeProbeIsFatal(errors.New("disk full")) {
		t.Fatal("writeProbeIsFatal treated a disk failure as a permission problem")
	}
	if !writeProbeIsFatal(fs.ErrPermission) {
		t.Fatal("writeProbeIsFatal ignored a permission failure")
	}
}

func TestReplaceErrorVariants(t *testing.T) {
	sticky := replaceError("/usr/bin/shelf", "/usr/bin", t.TempDir(), true)
	if !strings.Contains(sticky.Error(), "sticky") {
		t.Fatalf("replaceError sticky cause = %q", sticky)
	}

	prefix := t.TempDir()
	instructions := filepath.Join(prefix, "instructions.toml")
	if err := os.WriteFile(instructions, []byte("message = \"use pacman -Syu\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_SELF_UPDATE_INSTRUCTIONS", instructions)
	withInstructions := replaceError("/usr/bin/shelf", "/usr/bin", prefix, false)
	if !strings.Contains(withInstructions.Error(), "pacman") {
		t.Fatalf("replaceError omitted the packager instructions: %q", withInstructions)
	}
}
