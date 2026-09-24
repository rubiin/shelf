package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountBrackets(t *testing.T) {
	tests := []struct {
		line string
		want int
	}{
		{"[abc]", 0},
		{"[abc", 1},
		{"abc]", -1},
		{"[[a]]", 0},
		{`"a[b" [`, 1},   // brackets inside a double-quoted string are ignored
		{`"a\"[b" [`, 1}, // an escaped quote does not end the string early
		{"'a[b' [", 1},   // single-quoted strings hide brackets too
		{"[a # b", 1},    // the scan stops at a comment
		{"# [", 0},       // a comment-only line contributes nothing
	}
	for _, test := range tests {
		if got := countBrackets(test.line); got != test.want {
			t.Errorf("countBrackets(%q) = %d, want %d", test.line, got, test.want)
		}
	}
}

func TestNormalizeTableHeader(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"[plugins.foo]", "[plugins.foo]"},
		{" [plugins. foo] # comment", "[plugins.foo]"},
		{`[plugins."my.foo"]`, "[plugins.my.foo]"},
		{"[[plugins.foo]]", ""},
		{"[plugins.foo", ""},
		{"[plugins.foo] trailing", ""},
		{`files = ["a"]`, ""},
	}
	for _, test := range tests {
		if got := normalizeTableHeader(test.line); got != test.want {
			t.Errorf("normalizeTableHeader(%q) = %q, want %q", test.line, got, test.want)
		}
	}
}

func TestStripTrailingComment(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{`value = "a#b" # real`, `value = "a#b" `},
		{`value = "a\"#b" # real`, `value = "a\"#b" `},
		{"value = 'a#b' # real", "value = 'a#b' "},
		{"plain # comment", "plain "},
		{"no comment", "no comment"},
	}
	for _, test := range tests {
		if got := stripTrailingComment(test.line); got != test.want {
			t.Errorf("stripTrailingComment(%q) = %q, want %q", test.line, got, test.want)
		}
	}
}

func TestIsInlineTablePluginKey(t *testing.T) {
	tests := []struct {
		line, name string
		want       bool
	}{
		{`demo = { github = "a/b" }`, "demo", true},
		{`demo = { github = "a/b" }`, "other", false},
		{`"my.demo" = { github = "a/b" }`, "my.demo", true},
		{"demo = 5", "demo", false},
		{"demo", "demo", false},
	}
	for _, test := range tests {
		if got := isInlineTablePluginKey(test.line, test.name); got != test.want {
			t.Errorf("isInlineTablePluginKey(%q, %q) = %t, want %t", test.line, test.name, got, test.want)
		}
	}
}

func TestValidEnvironmentName(t *testing.T) {
	for _, name := range []string{"FOO", "foo", "FOO2", "_private", "a_b_c9"} {
		if !validEnvironmentName(name) {
			t.Errorf("validEnvironmentName(%q) = false", name)
		}
	}
	for _, name := range []string{"", "9foo", "-foo", "foo-bar", "foo bar"} {
		if validEnvironmentName(name) {
			t.Errorf("validEnvironmentName(%q) = true", name)
		}
	}
}

func TestValidateInlinePluginRejectsDepth(t *testing.T) {
	// Validate() can never reach this check: an inline plugin with a depth also
	// trips the earlier "depth needs a git source" rule, so exercise it directly.
	depth := 2
	err := validateInlinePlugin("demo", RawPlugin{Inline: "echo hi", Depth: &depth})
	if err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("depth on an inline plugin error = %v", err)
	}
}

func TestWriteVerifiedRejectsUnterminatedString(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "shell = \"zsh\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeVerified(path, []byte("shell = \"zsh")); err == nil {
		t.Fatal("writeVerified accepted TOML that does not decode")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != original {
		t.Fatalf("writeVerified touched the file: %q", contents)
	}
}

func TestWriteAtomicallyFailsWithoutConfigDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "config.toml")
	if err := writeAtomically(path, []byte("shell = \"zsh\"\n")); err == nil {
		t.Fatal("writeAtomically created its own directory")
	}
}
