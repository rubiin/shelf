package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"shelf/internal/lock"
	"shelf/internal/render"
)

// benchPlugins and benchMixedBlocks size the synthetic plugin set. Each plugin file mixes
// aliases, exports, function definitions, and control-flow blocks so parsing dominates the
// eval cost the way it does for real plugins; the sizes keep one full eval near 150ms.
const (
	benchPlugins     = 40
	benchMixedBlocks = 60
	benchMinSamples  = 5
)

// benchPluginBody returns a realistic mix of zsh constructs for plugin index i.
func benchPluginBody(i int) string {
	var content strings.Builder
	for block := range benchMixedBlocks {
		fmt.Fprintf(&content, "alias a_%d_%d=\"git status --short\"\nexport E_%d_%d=\"value-%d\"\n", i, block, i, block, block)
		fmt.Fprintf(&content, "function fn_%d_%d() {\n  local v\n  v=$(printf %%s \"value-%d\")\n  [[ -n \"$v\" ]] && print -r -- \"$v\"\n}\n", i, block, block)
		fmt.Fprintf(&content, "if [[ -n \"$HOME\" ]]; then\n  case \"$HOST\" in\n    a) : ;;\n    *) : ;;\n  esac\nfi\n")
	}
	return content.String()
}

// BenchmarkPluginScriptEvalZsh measures the cost of evaluating the emitted shelf script
// in a fresh zsh per operation, mirroring shell startup. It compares three states:
//
//   - plain-source: apply is the default `source` only; zsh re-parses every file as text
//     on every startup (the "before" state).
//   - zcompile-warm: apply is ["zcompile"]; the .zwc files already exist, so each startup
//     pays the guard's stat checks and then loads compiled bytecode.
//   - zcompile-cold: apply is ["zcompile"] but the .zwc files are deleted before each
//     operation; the guard must compile every file on first load.
//
// Run with, for example:
//
//	go test ./internal/cli -run '^$' -bench BenchmarkPluginScriptEvalZsh -benchtime=30x
func BenchmarkPluginScriptEvalZsh(b *testing.B) {
	directory := b.TempDir()
	sourceDirectory := filepath.Join(directory, "plugins")
	files := make([]string, 0, benchPlugins)
	plugins := make([]lock.LockedPlugin, 0, benchPlugins)
	for index := range benchPlugins {
		dir := filepath.Join(sourceDirectory, fmt.Sprintf("p%d", index))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		file := filepath.Join(dir, fmt.Sprintf("p%d.zsh", index))
		if err := os.WriteFile(file, []byte(benchPluginBody(index)), 0o600); err != nil {
			b.Fatal(err)
		}
		files = append(files, file)
		plugins = append(plugins, lock.LockedPlugin{Name: fmt.Sprintf("p%d", index), Directory: dir, Files: []string{file}})
	}

	plain, err := render.Script(lock.LockedConfig{Plugins: plugins}, "zsh")
	if err != nil {
		b.Fatal(err)
	}
	for index := range plugins {
		plugins[index].Apply = []string{"zcompile"}
	}
	guarded, err := render.Script(lock.LockedConfig{Plugins: plugins}, "zsh")
	if err != nil {
		b.Fatal(err)
	}

	evalZsh := func(script string) {
		b.Helper()
		if err := exec.Command("zsh", "-fc", `eval "$1"`, "shelf-test", script).Run(); err != nil {
			b.Fatal(err)
		}
	}

	removeZWCs := func(b *testing.B) {
		b.Helper()
		for _, file := range files {
			if err := os.Remove(file + ".zwc"); err != nil && !os.IsNotExist(err) {
				b.Fatal(err)
			}
		}
	}

	samples := make([]time.Duration, 0, benchMinSamples)
	for _, mode := range []string{"plain-source", "zcompile-warm", "zcompile-cold"} {
		mode := mode
		b.Run(mode, func(b *testing.B) {
			script := plain
			if mode != "plain-source" {
				script = guarded
			}
			b.StopTimer()
			switch mode {
			case "plain-source", "zcompile-cold":
				removeZWCs(b)
			case "zcompile-warm":
				evalZsh(guarded) // recreate the .zwc files the plain state removed.
			}
			b.StartTimer()
			samples = samples[:0]
			for b.Loop() {
				if mode == "zcompile-cold" {
					removeZWCs(b) // every op is a first load: compile, then source.
				}
				start := time.Now()
				evalZsh(script)
				samples = append(samples, time.Since(start))
			}
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			median := samples[len(samples)/2]
			b.ReportMetric(float64(median)/float64(time.Millisecond), "median-ms/op")
			b.ReportMetric(float64(samples[0])/float64(time.Millisecond), "min-ms/op")
			if len(samples) < benchMinSamples {
				b.Fatalf("need at least %d samples, got %d", benchMinSamples, len(samples))
			}
		})
	}
}
