package config

import (
	"strings"
	"testing"
)

func TestDecodeGlobalOptions(t *testing.T) {
	cfg, err := decode([]byte("profile = \"work\"\ncolor = \"never\"\nquiet = true\nverbose = false\nnon_interactive = true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "work" || cfg.Color != "never" {
		t.Fatalf("string options = profile %q color %q", cfg.Profile, cfg.Color)
	}
	if cfg.Quiet == nil || !*cfg.Quiet {
		t.Fatalf("quiet = %v, want true", cfg.Quiet)
	}
	if cfg.Verbose == nil || *cfg.Verbose {
		t.Fatalf("verbose = %v, want false", cfg.Verbose)
	}
	if cfg.NonInteractive == nil || !*cfg.NonInteractive {
		t.Fatalf("non_interactive = %v, want true", cfg.NonInteractive)
	}
}

func TestDecodeLeavesAbsentOptionsUnset(t *testing.T) {
	// A pointer field distinguishes "absent" from an explicit false.
	cfg, err := decode([]byte("shell = \"zsh\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Quiet != nil || cfg.Verbose != nil || cfg.NonInteractive != nil {
		t.Fatalf("absent options decoded as set: %+v", cfg)
	}
}

func TestValidateColorOption(t *testing.T) {
	for _, color := range Colors {
		if err := Validate(Config{Color: color}); err != nil {
			t.Errorf("Validate(color=%q) = %v", color, err)
		}
	}
	err := Validate(Config{Color: "rainbow"})
	if err == nil || !strings.Contains(err.Error(), "unsupported color") {
		t.Fatalf("Validate(rainbow) = %v, want an unsupported-color error", err)
	}
}
