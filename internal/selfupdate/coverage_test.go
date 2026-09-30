package selfupdate

import (
	"context"
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
