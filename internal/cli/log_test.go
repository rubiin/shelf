package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerVerboseWarning(t *testing.T) {
	var buffer bytes.Buffer
	log := logger{diagnostics: &buffer, colors: colors{enabled: false}, verbose: true}
	log.verboseWarning("Removed", "plugins/stale")
	if got := buffer.String(); !strings.Contains(got, "Removed plugins/stale") {
		t.Fatalf("verbose warning output = %q, want a Removed status", got)
	}

	// Verbose statuses stay silent without --verbose or under --quiet.
	var silent bytes.Buffer
	quiet := logger{diagnostics: &silent, verbose: true, quiet: true}
	quiet.verboseWarning("Removed", "x")
	quiet.status("Checked", "y")
	if silent.Len() != 0 {
		t.Fatalf("quiet logger printed output: %q", silent.String())
	}
	laconic := logger{diagnostics: &silent, verbose: false}
	laconic.verboseWarning("Removed", "x")
	laconic.verboseHeader("Unlocked", "z")
	if silent.Len() != 0 {
		t.Fatalf("non-verbose logger printed output: %q", silent.String())
	}
}

func TestLoggerWarning(t *testing.T) {
	var buffer bytes.Buffer
	log := logger{diagnostics: &buffer, colors: colors{enabled: false}}
	log.warning("Warning", "a profile matches no plugins")
	if !strings.Contains(buffer.String(), "Warning a profile matches no plugins") {
		t.Fatalf("warning output = %q", buffer.String())
	}

	// --quiet suppresses even warnings.
	silent := logger{diagnostics: &buffer, quiet: true}
	silent.warning("Warning", "gone")
	if !strings.Contains(buffer.String(), "a profile matches no plugins") {
		t.Fatal("quiet warning swallowed the visible warning")
	}
	if strings.Contains(buffer.String(), "gone") {
		t.Fatalf("quiet warning printed: %q", buffer.String())
	}
}
