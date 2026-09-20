package lock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelectFilesUsesFirstMatchForShellDefaults(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{
		"install_test_zsh.sh",
		"zsh-autosuggestions.plugin.zsh",
		"zsh-autosuggestions.zsh",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("echo test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := selectFiles(directory, "zsh-autosuggestions", "zsh", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(directory, "zsh-autosuggestions.plugin.zsh")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s]", got, want)
	}
}

func TestSelectFilesUsesShellSpecificDefaultPriority(t *testing.T) {
	tests := []struct {
		name  string
		shell string
		files []string
		want  string
	}{
		{
			name:  "zsh plugin file wins",
			shell: "zsh",
			files: []string{"demo.plugin.zsh", "demo.zsh", "demo.sh"},
			want:  "demo.plugin.zsh",
		},
		{
			name:  "bash plugin file wins",
			shell: "bash",
			files: []string{"demo.plugin.bash", "demo.plugin.sh", "demo.bash", "demo.sh"},
			want:  "demo.plugin.bash",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			for _, name := range test.files {
				if err := os.WriteFile(filepath.Join(directory, name), []byte("echo test\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			got, err := selectFiles(directory, "demo", test.shell, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(directory, test.want)
			if len(got) != 1 || got[0] != want {
				t.Fatalf("got %v, want [%s]", got, want)
			}
		})
	}
}

func TestSelectFilesCollectsExplicitUsePatterns(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{
		"README.md",
		"demo.plugin.zsh",
		"demo.zsh",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("echo test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := selectFiles(directory, "demo", "zsh", []string{"*.zsh", "*.md"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected all explicit matches, got %v", got)
	}
}

func TestSelectFilesMatchesNestedAndHiddenPaths(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"demo.zsh", ".hidden.zsh", "sub/nested.zsh"} {
		if err := os.WriteFile(filepath.Join(directory, filepath.FromSlash(name)), []byte("echo test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := selectFiles(directory, "demo", "zsh", []string{"**/*.zsh"}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(directory, ".hidden.zsh"), filepath.Join(directory, "demo.zsh"), filepath.Join(directory, "sub", "nested.zsh")}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSelectFilesMatchesPatternsWithLeadingDotSlash(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "demo.zsh"), []byte("echo test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := selectFiles(directory, "demo", "zsh", []string{"./*.zsh"}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(directory, "demo.zsh")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s]", got, want)
	}
}

func TestCollectFilesReportsUnreadableDirectory(t *testing.T) {
	if files, err := collectFiles(filepath.Join(t.TempDir(), "absent")); err != nil || files != nil {
		t.Fatalf("missing directory = %v, err = %v, want no files and no error", files, err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	closed := filepath.Join(t.TempDir(), "closed")
	if err := os.MkdirAll(closed, 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := collectFiles(closed); err == nil {
		t.Fatal("an unreadable directory was treated as empty")
	}
}

func TestSelectFilesSkipsDirectoriesNamedLikeMatches(t *testing.T) {
	// Only files are sourceable, so a directory named like a pattern must not be selected.
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "demo.zsh"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := selectFiles(directory, "demo", "zsh", []string{"*.zsh"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("directory matched a file pattern: %v", got)
	}
}

func TestSelectFilesIgnoresMissingDirectory(t *testing.T) {
	got, err := selectFiles(filepath.Join(t.TempDir(), "absent"), "demo", "zsh", []string{"*.zsh"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want no matches", got)
	}
}

func TestSelectFilesRejectsInvalidPattern(t *testing.T) {
	if _, err := selectFiles(t.TempDir(), "demo", "zsh", []string{"[unclosed"}, false); err == nil {
		t.Fatal("invalid pattern was accepted")
	}
}

func TestSelectFilesDeduplicatesExplicitMatches(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"demo.zsh", "demo.sh"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("echo test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := selectFiles(directory, "demo", "zsh", []string{"*.zsh", "demo.*", "*.sh"}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Overlapping patterns select a file once, ordered by file name across every pattern.
	want := []string{filepath.Join(directory, "demo.sh"), filepath.Join(directory, "demo.zsh")}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
