package cli

import (
	"os"
	"path/filepath"
	"testing"

	"shelf/internal/config"
)

func boolPtr(value bool) *bool { return &value }

// saveGlobals pins the package-level option vars so a test can mutate them freely.
func saveGlobals(t *testing.T) {
	t.Helper()
	originalColor, originalProfile := color, profile
	originalQuiet, originalVerbose, originalNonInteractive := quiet, verbose, nonInteractive
	t.Cleanup(func() {
		color, profile = originalColor, originalProfile
		quiet, verbose, nonInteractive = originalQuiet, originalVerbose, originalNonInteractive
	})
}

func TestApplyConfigDefaultsOverridesEnvironment(t *testing.T) {
	saveGlobals(t)
	t.Setenv("SHELF_COLOR", "auto")
	t.Setenv("SHELF_QUIET", "false")
	cmd := NewRoot()
	// The flag defaults seed the globals from the environment; config value must win over them.
	color, quiet = "auto", false
	applyConfigDefaults(cmd, config.Config{Color: "never", Profile: "work", Quiet: boolPtr(true), Verbose: boolPtr(true)})
	if color != "never" {
		t.Errorf("color = %q, want the config value never", color)
	}
	if profile != "work" {
		t.Errorf("profile = %q, want the config value work", profile)
	}
	if !quiet || !verbose {
		t.Errorf("quiet/verbose = %v/%v, want both true from config", quiet, verbose)
	}
}

func TestApplyConfigDefaultsKeepsExplicitFlags(t *testing.T) {
	saveGlobals(t)
	cmd := NewRoot()
	if err := cmd.ParseFlags([]string{"--color", "always", "--profile", "cli", "--quiet"}); err != nil {
		t.Fatal(err)
	}
	applyConfigDefaults(cmd, config.Config{Color: "never", Profile: "work", Quiet: boolPtr(false)})
	if color != "always" || profile != "cli" || !quiet {
		t.Fatalf("explicit flags overridden by config: color=%q profile=%q quiet=%v", color, profile, quiet)
	}
}

func TestLoadConfigDefaultsReadsTheConfigFile(t *testing.T) {
	saveGlobals(t)
	directory := t.TempDir()
	file := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(file, []byte("color = \"never\"\nprofile = \"work\"\nnon_interactive = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELF_CONFIG_FILE", file)
	cmd := NewRoot()
	loadConfigDefaults(cmd)
	if color != "never" || profile != "work" || !nonInteractive {
		t.Fatalf("config defaults not applied: color=%q profile=%q nonInteractive=%v", color, profile, nonInteractive)
	}
}

func TestLoadConfigDefaultsIgnoresMissingConfig(t *testing.T) {
	saveGlobals(t)
	color, profile = "auto", ""
	t.Setenv("SHELF_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.toml"))
	loadConfigDefaults(NewRoot())
	if color != "auto" || profile != "" {
		t.Fatalf("missing config changed options: color=%q profile=%q", color, profile)
	}
}
