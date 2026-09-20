package lock

import (
	"context"
	"path/filepath"
	"testing"

	"shelf/internal/config"
	"shelf/internal/source"
)

type testInstaller struct {
	directory string
}

func (installer testInstaller) Install(_ context.Context, request source.Request) (source.Installed, error) {
	return source.Installed{
		Directory: installer.directory,
		File:      filepath.Join(installer.directory, request.Name+".plugin.zsh"),
	}, nil
}

func TestBuildPreservesPluginDeclarationOrder(t *testing.T) {
	cfg := config.Config{
		Plugins: map[string]config.RawPlugin{
			"zsh-vi-mode": {Inline: "echo vi"},
			"zsh-defer":   {Inline: "echo defer"},
		},
		PluginOrder: []string{"zsh-defer", "zsh-vi-mode"},
	}

	locked, err := Build(Context{Shell: "zsh"}, cfg, testInstaller{directory: t.TempDir()}, ModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked.Plugins) != 2 || locked.Plugins[0].Name != "zsh-defer" || locked.Plugins[1].Name != "zsh-vi-mode" {
		t.Fatalf("plugin order = %v", []string{locked.Plugins[0].Name, locked.Plugins[1].Name})
	}
}
