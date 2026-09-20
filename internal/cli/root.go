package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"shelf/internal/config"
	"shelf/internal/lock"
	"shelf/internal/render"
	"shelf/internal/source"
)

var (
	quiet          bool
	nonInteractive bool
	verbose        bool
	color          string
	configDir      string
	dataDir        string
	configFile     string
	profile        string
)

// Context contains the resolved runtime settings shared by commands.
type Context struct {
	ConfigFile      string
	ConfigDirectory string
	DataDirectory   string
	Profile         string
	Shell           string
	Quiet           bool
	NonInteractive  bool
	Verbose         bool
	Color           string
}

func NewRoot() *cobra.Command {
	command := &cobra.Command{
		Use:   "shelf",
		Short: "Manage shell plugins",
	}
	command.SetOut(os.Stdout)
	command.SetErr(os.Stderr)
	command.PersistentFlags().BoolVar(&quiet, "quiet", envBool("SHELF_QUIET"), "suppress diagnostics")
	command.PersistentFlags().BoolVar(&nonInteractive, "non-interactive", envBool("SHELF_NON_INTERACTIVE"), "disable interactive prompts")
	command.PersistentFlags().BoolVar(&verbose, "verbose", envBool("SHELF_VERBOSE"), "enable verbose diagnostics")
	command.PersistentFlags().StringVar(&color, "color", envString("SHELF_COLOR", "auto"), "color output: auto, always, or never")
	command.PersistentFlags().StringVar(&configDir, "config-dir", envString("SHELF_CONFIG_DIR", ""), "configuration directory")
	command.PersistentFlags().StringVar(&dataDir, "data-dir", envString("SHELF_DATA_DIR", ""), "data directory")
	command.PersistentFlags().StringVar(&configFile, "config-file", envString("SHELF_CONFIG_FILE", ""), "configuration file")
	command.PersistentFlags().StringVar(&profile, "profile", envString("SHELF_PROFILE", ""), "profile to use")

	var update, reinstall bool
	lockCommand := &cobra.Command{
		Use:   "lock",
		Short: "Install plugin sources and write the lock file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode := lock.ModeNormal
			if update {
				mode = lock.ModeUpdate
			}
			if reinstall {
				mode = lock.ModeReinstall
			}
			return lockConfig(mode, cmd.ErrOrStderr())
		},
	}
	lockCommand.Flags().BoolVar(&update, "update", false, "update plugin sources")
	lockCommand.Flags().BoolVar(&reinstall, "reinstall", false, "reinstall plugin sources")
	lockCommand.MarkFlagsMutuallyExclusive("update", "reinstall")
	command.AddCommand(lockCommand)

	var relock, sourceUpdate, sourceReinstall bool
	sourceCommand := &cobra.Command{
		Use:   "source",
		Short: "Generate shell code from locked plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode := lock.ModeNormal
			if sourceUpdate {
				mode = lock.ModeUpdate
			}
			if sourceReinstall {
				mode = lock.ModeReinstall
			}
			return sourceConfig(cmd.OutOrStdout(), relock || sourceUpdate || sourceReinstall, mode)
		},
	}
	sourceCommand.Flags().BoolVar(&relock, "relock", false, "regenerate lock file")
	sourceCommand.Flags().BoolVar(&sourceUpdate, "update", false, "update plugin sources")
	sourceCommand.Flags().BoolVar(&sourceReinstall, "reinstall", false, "reinstall plugin sources")
	sourceCommand.MarkFlagsMutuallyExclusive("update", "reinstall")
	command.AddCommand(sourceCommand)
	var addGitHub, addGit, addGist, addRemote, addLocal, addInline string
	var addRev, addBranch, addTag, addProtocol, addDir, addFile string
	var addUse, addApply, addProfiles []string
	var addHooks map[string]string
	addCommand := &cobra.Command{
		Use:   "add NAME",
		Short: "Add a plugin to the configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
			if err != nil {
				return err
			}
			return config.Add(paths.ConfigFile, args[0], config.RawPlugin{GitHub: addGitHub, Git: addGit, Gist: addGist, Remote: addRemote, Local: addLocal, Inline: addInline, Rev: addRev, Branch: addBranch, Tag: addTag, Protocol: addProtocol, Dir: addDir, File: addFile, Use: addUse, Apply: addApply, Profiles: addProfiles, Hooks: addHooks})
		},
	}
	addCommand.Flags().StringVar(&addGitHub, "github", "", "GitHub repository")
	addCommand.Flags().StringVar(&addGit, "git", "", "Git repository")
	addCommand.Flags().StringVar(&addGist, "gist", "", "GitHub Gist")
	addCommand.Flags().StringVar(&addRemote, "remote", "", "remote URL")
	addCommand.Flags().StringVar(&addLocal, "local", "", "local path")
	addCommand.Flags().StringVar(&addInline, "inline", "", "inline plugin content")
	addCommand.Flags().StringVar(&addRev, "rev", "", "Git revision")
	addCommand.Flags().StringVar(&addBranch, "branch", "", "Git branch")
	addCommand.Flags().StringVar(&addTag, "tag", "", "Git tag")
	addCommand.Flags().StringVar(&addProtocol, "protocol", "", "Git protocol: https, git, or ssh")
	addCommand.Flags().StringVar(&addDir, "dir", "", "plugin subdirectory")
	addCommand.Flags().StringVar(&addFile, "file", "", "plugin file")
	addCommand.Flags().StringSliceVar(&addUse, "use", nil, "plugin file glob")
	addCommand.Flags().StringSliceVar(&addApply, "apply", nil, "template names")
	addCommand.Flags().StringSliceVar(&addProfiles, "profiles", nil, "plugin profiles")
	addCommand.Flags().StringToStringVar(&addHooks, "hooks", nil, "plugin hooks")
	command.AddCommand(addCommand)

	command.AddCommand(&cobra.Command{Use: "edit", Short: "Open the configuration in an editor", RunE: func(_ *cobra.Command, _ []string) error { return editConfig() }})
	command.AddCommand(&cobra.Command{Use: "remove NAME", Short: "Remove a plugin from the configuration", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
		if err != nil {
			return err
		}
		return config.Remove(paths.ConfigFile, args[0])
	}})
	command.AddCommand(&cobra.Command{Use: "completions SHELL", Short: "Generate shell completion scripts", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return command.GenBashCompletion(cmd.OutOrStdout())
		case "zsh":
			return command.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return command.GenFishCompletion(cmd.OutOrStdout(), true)
		default:
			return fmt.Errorf("unsupported completion shell: %s", args[0])
		}
	}})
	var initShell string
	initCommand := &cobra.Command{
		Use:   "init",
		Short: "Create a new shell plugin configuration",
		RunE: func(_ *cobra.Command, _ []string) error {
			paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
			if err != nil {
				return err
			}
			shell := configShell()
			if initShell != "" {
				shell = config.Shell(initShell)
			}
			return config.Initialize(paths.ConfigFile, shell)
		},
	}
	initCommand.Flags().StringVar(&initShell, "shell", "", "shell: bash or zsh")
	command.AddCommand(initCommand)
	command.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print shelf version information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "shelf version dev")
			return err
		},
	})
	return command
}

func lockConfig(mode lock.Mode, diagnostics io.Writer) error {
	paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	colors := newColors(color, diagnostics)
	if !quiet {
		_, _ = fmt.Fprintf(diagnostics, "%s %s\n", colors.header("Loaded"), displayPath(paths.ConfigFile))
		for _, plugin := range cfg.Plugins {
			_, _ = fmt.Fprintf(diagnostics, "%s %s\n", colors.status("Checked"), pluginSource(plugin))
		}
	}
	shell := cfg.Shell
	if shell == "" {
		shell = configShell()
	}
	locked, err := lock.Build(lock.Context{ConfigFile: paths.ConfigFile, DataDirectory: paths.DataDirectory, Profile: profile, Shell: string(shell)}, cfg, source.NewInstaller(paths.DataDirectory), mode)
	if err != nil {
		return err
	}
	lockPath := filepath.Join(paths.ConfigDirectory, "plugins.lock")
	if err := lock.Write(lockPath, locked); err != nil {
		return err
	}
	if !quiet {
		_, _ = fmt.Fprintf(diagnostics, "%s %s\n", colors.header("Locked"), displayPath(lockPath))
	}
	return nil
}

func pluginSource(plugin config.RawPlugin) string {
	switch {
	case plugin.GitHub != "":
		return "https://github.com/" + plugin.GitHub
	case plugin.Git != "":
		return plugin.Git
	case plugin.Gist != "":
		return "https://gist.github.com/" + plugin.Gist
	case plugin.Remote != "":
		return plugin.Remote
	case plugin.Local != "":
		return plugin.Local
	default:
		return "inline"
	}
}

func displayPath(path string) string {
	home := homeDir()
	if home != "" && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func editConfig() error {
	paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
	if err != nil {
		return err
	}
	editor := envString("SHELF_EDITOR", envString("VISUAL", envString("EDITOR", "")))
	if editor == "" {
		return fmt.Errorf("no editor configured")
	}
	parts := strings.Fields(editor)
	command := exec.Command(parts[0], append(parts[1:], paths.ConfigFile)...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func sourceConfig(output io.Writer, force bool, mode lock.Mode) error {
	paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	shell := cfg.Shell
	if shell == "" {
		shell = configShell()
	}
	lockContext := lock.Context{ConfigFile: paths.ConfigFile, DataDirectory: paths.DataDirectory, Profile: profile, Shell: string(shell)}
	lockPath := filepath.Join(paths.ConfigDirectory, "plugins.lock")
	var locked lock.LockedConfig
	if !force {
		locked, err = lock.Read(lockPath)
		if err == nil {
			var valid bool
			valid, err = lock.Verify(lockPath, lockContext)
			if err == nil && !valid {
				err = os.ErrNotExist
			}
		}
	}
	if force || err != nil {
		locked, err = lock.Build(lockContext, cfg, source.NewInstaller(paths.DataDirectory), mode)
		if err != nil {
			return err
		}
		if err := lock.Write(lockPath, locked); err != nil {
			return err
		}
	} else if err := lock.Restore(cfg, source.NewInstaller(paths.DataDirectory), locked); err != nil {
		return err
	}
	script, err := render.Script(locked, string(shell), cfg.Templates)
	if err != nil {
		return err
	}
	_, err = io.WriteString(output, script)
	return err
}

func configShell() config.Shell {
	if value := os.Getenv("SHELF_SHELL"); value == "bash" {
		return config.Bash
	}
	if value := os.Getenv("SHELF_SHELL"); value == "zsh" {
		return config.Zsh
	}
	return config.Zsh
}

func Execute(args []string, stdout, stderr io.Writer) error {
	command := NewRoot()
	command.SetArgs(args)
	command.SetOut(stdout)
	command.SetErr(stderr)
	return command.Execute()
}

func RuntimeContext() Context {
	configDirectory := configDir
	if configDirectory == "" {
		configDirectory = filepath.Join(envString("XDG_CONFIG_HOME", filepath.Join(homeDir(), ".config")), "shelf")
	}
	dataDirectory := dataDir
	if dataDirectory == "" {
		dataDirectory = filepath.Join(envString("XDG_DATA_HOME", filepath.Join(homeDir(), ".local", "share")), "shelf")
	}
	resolvedConfigFile := configFile
	if resolvedConfigFile == "" {
		resolvedConfigFile = filepath.Join(configDirectory, "config.toml")
	}
	return Context{ConfigFile: resolvedConfigFile, ConfigDirectory: configDirectory, DataDirectory: dataDirectory, Profile: profile, Quiet: quiet, NonInteractive: nonInteractive, Verbose: verbose, Color: color}
}

func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string) bool { return os.Getenv(name) == "1" || os.Getenv(name) == "true" }

func homeDir() string {
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	home, _ := os.UserHomeDir()
	return home
}
