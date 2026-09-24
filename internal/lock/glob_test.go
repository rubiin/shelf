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

	got, err := selectFiles(directory, "zsh-autosuggestions", "zsh", nil, true, nil)
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

			got, err := selectFiles(directory, "demo", test.shell, nil, true, nil)
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

	got, err := selectFiles(directory, "demo", "zsh", []string{"*.zsh", "*.md"}, false, nil)
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

	got, err := selectFiles(directory, "demo", "zsh", []string{"**/*.zsh"}, false, nil)
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

	got, err := selectFiles(directory, "demo", "zsh", []string{"./*.zsh"}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(directory, "demo.zsh")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s]", got, want)
	}
}

func TestCollectFilesSkipsGitMetadata(t *testing.T) {
	// A plugin's .git directory is walked for nothing: no source pattern can match it.
	directory := t.TempDir()
	for _, name := range []string{
		"demo.zsh",
		".git/hooks/pre-commit.sh",
		".git/objects/pack/pack-abc123.idx",
		".git/refs/heads/main",
		".git/demo.zsh",
	} {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	files, err := collectFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "demo.zsh" {
		t.Fatalf("files = %v, want only [demo.zsh]", files)
	}

	// And through selection, where a .git file must never be picked up by a pattern.
	got, err := selectFiles(directory, "demo", "zsh", []string{"*.zsh", "*.sh"}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(directory, "demo.zsh")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("selected %v, want [%s]", got, want)
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

	got, err := selectFiles(directory, "demo", "zsh", []string{"*.zsh"}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("directory matched a file pattern: %v", got)
	}
}

func TestSelectFilesIgnoresMissingDirectory(t *testing.T) {
	got, err := selectFiles(filepath.Join(t.TempDir(), "absent"), "demo", "zsh", []string{"*.zsh"}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want no matches", got)
	}
}

func TestSelectFilesRejectsInvalidPattern(t *testing.T) {
	if _, err := selectFiles(t.TempDir(), "demo", "zsh", []string{"[unclosed"}, false, nil); err == nil {
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

	got, err := selectFiles(directory, "demo", "zsh", []string{"*.zsh", "demo.*", "*.sh"}, false, nil)
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

func TestSelectFilesAppliesIgnoreGlobs(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"demo.plugin.zsh", "test-helper.zsh", "tests/helper.zsh", "README.md"} {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("echo test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := selectFiles(directory, "demo", "zsh", []string{"**/*.zsh", "*.md"}, false, []string{"**/test*", "**/tests/*"})
	if err != nil {
		t.Fatal(err)
	}
	// test-helper.zsh matches **/test* and tests/helper.zsh matches **/tests/*, leaving the plugin file and the readme.
	want := []string{filepath.Join(directory, "README.md"), filepath.Join(directory, "demo.plugin.zsh")}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSelectFilesAppliesIgnoreToDefaultMatches(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"demo.plugin.zsh", "demo.sh"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("echo test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Default matches stop at the first selecting pattern, then the ignore pass drops it as well.
	got, err := selectFiles(directory, "demo", "zsh", nil, true, []string{"*.plugin.zsh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want an empty selection when the default match is ignored", got)
	}
}

func TestSelectFilesIgnoreCanEmptyTheSelection(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"demo.zsh", "demo.plugin.zsh"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("echo test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := selectFiles(directory, "demo", "zsh", []string{"*.zsh"}, false, []string{"*.zsh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want an empty selection when everything is ignored", got)
	}
}

func TestSelectFilesIgnoreSupportsNameSubstitution(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"demo.plugin.zsh", "demo.zsh"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("echo test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := selectFiles(directory, "demo", "zsh", []string{"*.zsh"}, false, []string{"{{ name }}.plugin.zsh"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(directory, "demo.zsh")}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSelectFilesRejectsInvalidIgnorePattern(t *testing.T) {
	if _, err := selectFiles(t.TempDir(), "demo", "zsh", []string{"*.zsh"}, false, []string{"[unclosed"}); err == nil {
		t.Fatal("invalid ignore pattern was accepted")
	}
}

func TestSelectFilesEscapesPluginNameMetacharacters(t *testing.T) {
	// A plugin name is spliced into every pattern, so its metacharacters must
	// select the name literally instead of widening the glob.
	tests := []struct {
		name  string
		file  string
		decoy string // matches only if the name's metacharacters act as glob syntax
	}{
		{name: "star*star", file: "star*star.plugin.zsh", decoy: "starABstar.plugin.zsh"},
		{name: "q?mark", file: "q?mark.plugin.zsh", decoy: "qxmark.plugin.zsh"},
		{name: "b[cd]", file: "b[cd].plugin.zsh", decoy: "bd.plugin.zsh"},
		{name: "bra{ce}", file: "bra{ce}.plugin.zsh", decoy: "brace.plugin.zsh"},
		{name: `slash\name`, file: `slash\name.plugin.zsh`, decoy: `slashxname.plugin.zsh`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, test.file), []byte("echo test\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, test.decoy), []byte("echo decoy\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := selectFiles(directory, test.name, "zsh", []string{"{{ name }}.plugin.zsh"}, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(directory, test.file)
			if len(got) != 1 || got[0] != want {
				t.Fatalf("selected %v, want only the literal name %s", got, want)
			}
		})
	}
}

func TestCollectFilesFollowsSymlinkedRoot(t *testing.T) {
	// A stow-style local source is a symlink to the packaged plugin; the walk
	// must descend into the target directory instead of reporting the link as
	// a lone "file".
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "demo.plugin.zsh"), []byte("echo test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "plugin")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	files, err := collectFiles(link)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "demo.plugin.zsh" {
		t.Fatalf("files = %v, want [demo.plugin.zsh]", files)
	}

	got, err := selectFiles(link, "demo", "zsh", []string{"*.plugin.zsh"}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(target, "demo.plugin.zsh")
	// Join against the original symlink path; it resolves to the same target.
	if len(got) != 1 {
		t.Fatalf("selected %v, want the symlinked file", got)
	}
	resolved, err := filepath.EvalSymlinks(got[0])
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("selected %s, want %s", got[0], want)
	}
}

// A symlink loop makes os.Stat fail with ELOOP, which is not ErrNotExist, so the
// walk must report it instead of treating the directory as empty.
func TestCollectFilesReportsNonMissingStatFailure(t *testing.T) {
	directory := t.TempDir()
	loop := filepath.Join(directory, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	if _, err := collectFiles(loop); err == nil {
		t.Fatal("a symlink loop was treated as a missing directory")
	}
}

func TestSelectFilesReportsCollectorFailure(t *testing.T) {
	directory := t.TempDir()
	loop := filepath.Join(directory, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	if _, err := selectFiles(loop, "demo", "zsh", []string{"*.zsh"}, false, nil); err == nil {
		t.Fatal("a collect failure was swallowed by selection")
	}
}

func TestSelectFilesValidatesEveryPattern(t *testing.T) {
	// Validation must not stop at the first selecting pattern: an invalid glob
	// later in the list is a config error even when first-match already found
	// files.
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "demo.plugin.zsh"), []byte("echo test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := selectFiles(directory, "demo", "zsh", []string{"demo.plugin.zsh", "[unclosed"}, true, nil); err == nil {
		t.Fatal("an invalid pattern after a first-match selection was accepted")
	}
}
