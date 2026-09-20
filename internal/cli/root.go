package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/term"

	"github.com/spf13/cobra"
	"shelf/internal/config"
	"shelf/internal/lock"
	"shelf/internal/render"
	"shelf/internal/source"
	"shelf/internal/tui"
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
		Use:          "shelf",
		Short:        "Manage shell plugins",
		SilenceUsage: true,
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
	var lockConcurrency int
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
			return lockConfig(mode, lockConcurrency, cmd.ErrOrStderr())
		},
	}
	lockCommand.Flags().BoolVar(&update, "update", false, "update plugin sources")
	lockCommand.Flags().BoolVar(&reinstall, "reinstall", false, "reinstall plugin sources")
	lockCommand.Flags().IntVar(&lockConcurrency, "concurrency", lock.DefaultConcurrency, "maximum concurrent plugin installs")
	lockCommand.MarkFlagsMutuallyExclusive("update", "reinstall")
	command.AddCommand(lockCommand)

	var relock, sourceUpdate, sourceReinstall bool
	var sourceConcurrency int
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
			return sourceConfig(cmd.OutOrStdout(), relock || sourceUpdate || sourceReinstall, mode, sourceConcurrency)
		},
	}
	sourceCommand.Flags().BoolVar(&relock, "relock", false, "regenerate lock file")
	sourceCommand.Flags().BoolVar(&sourceUpdate, "update", false, "update plugin sources")
	sourceCommand.Flags().BoolVar(&sourceReinstall, "reinstall", false, "reinstall plugin sources")
	sourceCommand.Flags().IntVar(&sourceConcurrency, "concurrency", lock.DefaultConcurrency, "maximum concurrent plugin installs")
	sourceCommand.MarkFlagsMutuallyExclusive("update", "reinstall")
	command.AddCommand(sourceCommand)
	var updateLock bool
	var updateConcurrency int
	updateCommand := &cobra.Command{
		Use:   "update",
		Short: "Update plugin sources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if updateLock {
				return lockConfig(lock.ModeUpdate, updateConcurrency, cmd.ErrOrStderr())
			}
			return updateSources(cmd.OutOrStdout(), updateConcurrency)
		},
	}
	updateCommand.Flags().BoolVar(&updateLock, "lock", false, "write the refreshed lock file without shell output")
	updateCommand.Flags().IntVar(&updateConcurrency, "concurrency", lock.DefaultConcurrency, "maximum concurrent plugin installs")
	command.AddCommand(updateCommand)
	command.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print resolved Shelf paths",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printPaths(cmd.OutOrStdout())
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Check installed plugin status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return pluginStatus(cmd.OutOrStdout())
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check Shelf configuration and installation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doctor(cmd.OutOrStdout())
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "clean",
		Short: "Remove unconfigured installed plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cleanPlugins(cmd.OutOrStdout())
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listPlugins(cmd.OutOrStdout())
		},
	})
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
	var removeInteractive bool
	removeCommand := &cobra.Command{
		Use:   "remove [NAME]",
		Short: "Remove a plugin from the configuration",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
			if err != nil {
				return err
			}
			if removeInteractive {
				if len(args) > 0 {
					return fmt.Errorf("NAME cannot be combined with --interactive")
				}
				if nonInteractive {
					return fmt.Errorf("remove --interactive cannot be used with --non-interactive")
				}
				return removeInteractiveConfig(cmd, paths)
			}
			if len(args) == 0 {
				return fmt.Errorf("accepts 1 arg(s), received 0")
			}
			return config.Remove(paths.ConfigFile, args[0])
		},
	}
	removeCommand.Flags().BoolVarP(&removeInteractive, "interactive", "i", false, "select plugins to remove interactively")
	command.AddCommand(removeCommand)
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
			shell, err := configShell()
			if err != nil {
				return err
			}
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

func lockConfig(mode lock.Mode, concurrency int, diagnostics io.Writer) error {
	paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
	if err != nil {
		return err
	}
	cfg, fingerprint, err := loadConfigWithFingerprint(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	colors := newColors(color, diagnostics)
	if !quiet {
		_, _ = fmt.Fprintf(diagnostics, "%s %s\n", colors.header("Loaded"), displayPath(paths.ConfigFile))
		for _, name := range lock.PluginNames(cfg) {
			_, _ = fmt.Fprintf(diagnostics, "%s %s\n", colors.status("Checked"), pluginSource(cfg.Plugins[name]))
		}
	}
	shell, err := resolveShell(cfg)
	if err != nil {
		return err
	}
	locked, err := lock.BuildWithConcurrency(lock.Context{ConfigFile: paths.ConfigFile, ConfigFingerprint: fingerprint, DataDirectory: paths.DataDirectory, Profile: profile, Shell: string(shell)}, cfg, source.NewInstaller(paths.DataDirectory), mode, concurrency)
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
	arguments, err := splitEditorCommand(editor)
	if err != nil {
		return err
	}
	if len(arguments) == 0 {
		return fmt.Errorf("no editor configured")
	}
	command := exec.Command(arguments[0], append(arguments[1:], paths.ConfigFile)...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

// splitEditorCommand splits an editor command line with shell-word rules, honoring quotes and escapes.
func splitEditorCommand(value string) ([]string, error) {
	var (
		arguments []string
		current   strings.Builder
		quote     rune
		escaped   bool
		started   bool
	)
	for _, character := range value {
		switch {
		case escaped:
			current.WriteRune(character)
			escaped, started = false, true
		case character == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if character == quote {
				quote = 0
			} else {
				current.WriteRune(character)
			}
			started = true
		case character == '\'' || character == '"':
			quote, started = character, true
		case unicode.IsSpace(character):
			if started {
				arguments = append(arguments, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(character)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unbalanced quotes in editor command: %q", value)
	}
	if started {
		arguments = append(arguments, current.String())
	}
	return arguments, nil
}

func sourceConfig(output io.Writer, force bool, mode lock.Mode, concurrency int) error {
	paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
	if err != nil {
		return err
	}
	cfg, fingerprint, err := loadConfigWithFingerprint(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	shell, err := resolveShell(cfg)
	if err != nil {
		return err
	}
	lockContext := lock.Context{ConfigFile: paths.ConfigFile, ConfigFingerprint: fingerprint, DataDirectory: paths.DataDirectory, Profile: profile, Shell: string(shell)}
	lockPath := filepath.Join(paths.ConfigDirectory, "plugins.lock")
	var locked lock.LockedConfig
	if !force {
		locked, err = lock.Read(lockPath)
		if err == nil && !lock.VerifyLocked(locked, lockContext) {
			err = os.ErrNotExist
		}
	}
	if force || err != nil {
		locked, err = lock.BuildWithConcurrency(lockContext, cfg, source.NewInstaller(paths.DataDirectory), mode, concurrency)
		if err != nil {
			return err
		}
		if err := lock.Write(lockPath, locked); err != nil {
			return err
		}
	} else if err := lock.Restore(cfg, source.NewInstaller(paths.DataDirectory), locked, concurrency); err != nil {
		return err
	}
	script, err := render.Script(locked, string(shell), cfg.Templates)
	if err != nil {
		return err
	}
	_, err = io.WriteString(output, script)
	return err
}

func updateSources(output io.Writer, concurrency int) error {
	paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
	if err != nil {
		return err
	}
	cfg, fingerprint, err := loadConfigWithFingerprint(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	shell, err := resolveShell(cfg)
	if err != nil {
		return err
	}
	locked, err := lock.BuildWithConcurrency(lock.Context{ConfigFile: paths.ConfigFile, ConfigFingerprint: fingerprint, DataDirectory: paths.DataDirectory, Profile: profile, Shell: string(shell)}, cfg, source.NewInstaller(paths.DataDirectory), lock.ModeUpdate, concurrency)
	if err != nil {
		return err
	}
	script, err := render.Script(locked, string(shell), cfg.Templates)
	if err != nil {
		return err
	}
	_, err = io.WriteString(output, script)
	return err
}

// interactiveSelect is the picker behind remove --interactive; a variable so tests can script it.
var interactiveSelect = func(options []string, out io.Writer) ([]string, error) {
	return tui.Select(options, tui.IO{In: os.Stdin, Out: out, MakeRaw: term.MakeRaw, Restore: term.Restore, Color: colorEnabled(color, true)})
}

func removeInteractiveConfig(cmd *cobra.Command, paths Paths) error {
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	if len(cfg.Plugins) == 0 {
		return fmt.Errorf("no plugins configured")
	}
	names := make([]string, 0, len(cfg.PluginOrder))
	names = append(names, cfg.PluginOrder...)
	out := cmd.OutOrStdout()
	selection, err := interactiveSelect(names, out)
	if errors.Is(err, tui.ErrCancelled) {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "cancelled")
		return nil
	}
	if err != nil {
		return err
	}
	for _, name := range selection {
		if err := config.Remove(paths.ConfigFile, name); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "removed: %s\n", name)
	}
	if len(selection) > 0 {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "run 'shelf lock' to update the lock file")
	}
	return nil
}

func listPlugins(output io.Writer) error {
	paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return err
	}
	for _, name := range cfg.PluginOrder {
		if _, err := fmt.Fprintln(output, name); err != nil {
			return err
		}
	}
	return nil
}

func printPaths(output io.Writer) error {
	paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
	if err != nil {
		return err
	}
	for _, entry := range []struct {
		name string
		path string
	}{
		{"config_dir", paths.ConfigDirectory},
		{"data_dir", paths.DataDirectory},
		{"config_file", paths.ConfigFile},
		{"lock_file", filepath.Join(paths.ConfigDirectory, "plugins.lock")},
	} {
		if _, err := fmt.Fprintf(output, "%s=%s\n", entry.name, entry.path); err != nil {
			return err
		}
	}
	return nil
}

func pluginStatus(output io.Writer) error {
	paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
	if err != nil {
		return err
	}
	cfg, fingerprint, err := loadConfigWithFingerprint(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	shell, err := resolveShell(cfg)
	if err != nil {
		return err
	}
	lockPath := filepath.Join(paths.ConfigDirectory, "plugins.lock")
	valid, err := lock.Verify(lockPath, lock.Context{ConfigFile: paths.ConfigFile, ConfigFingerprint: fingerprint, DataDirectory: paths.DataDirectory, Profile: profile, Shell: string(shell)})
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("lockfile is stale or selected plugin files are missing")
	}
	locked, err := lock.Read(lockPath)
	if err != nil {
		return err
	}
	var unhealthy bool
	for _, plugin := range locked.Plugins {
		state := "ok"
		if plugin.Rev != "" {
			output, err := exec.Command("git", "-C", plugin.Directory, "rev-parse", "HEAD").Output()
			if err != nil {
				state = "unable to read revision"
			} else if revision := strings.TrimSpace(string(output)); revision != plugin.Rev {
				state = "revision " + revision + ", want " + plugin.Rev
			}
		}
		if state != "ok" {
			unhealthy = true
		}
		if _, err := fmt.Fprintf(output, "%s: %s\n", plugin.Name, state); err != nil {
			return err
		}
	}
	if unhealthy {
		return fmt.Errorf("installed plugins differ from the lockfile")
	}
	return nil
}

func doctor(output io.Writer) error {
	paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
	if err != nil {
		return err
	}
	cfg, fingerprint, err := loadConfigWithFingerprint(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "config: ok"); err != nil {
		return err
	}
	if usesGit(cfg) {
		if _, err := exec.LookPath("git"); err != nil {
			return fmt.Errorf("git is required for configured Git plugins: %w", err)
		}
		if _, err := fmt.Fprintln(output, "git: ok"); err != nil {
			return err
		}
	}
	shell, err := resolveShell(cfg)
	if err != nil {
		return err
	}
	valid, err := lock.Verify(filepath.Join(paths.ConfigDirectory, "plugins.lock"), lock.Context{ConfigFile: paths.ConfigFile, ConfigFingerprint: fingerprint, DataDirectory: paths.DataDirectory, Profile: profile, Shell: string(shell)})
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("lockfile is stale or selected plugin files are missing")
	}
	_, err = fmt.Fprintln(output, "lock: ok")
	return err
}

func usesGit(cfg config.Config) bool {
	for _, plugin := range cfg.Plugins {
		if plugin.Git != "" || plugin.GitHub != "" || plugin.Gist != "" {
			return true
		}
	}
	return false
}

func cleanPlugins(output io.Writer) error {
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
	pluginsDir := filepath.Join(paths.DataDirectory, "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, configured := cfg.Plugins[entry.Name()]; !entry.IsDir() || configured {
			continue
		}
		if err := os.RemoveAll(filepath.Join(pluginsDir, entry.Name())); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "removed: %s\n", entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

// loadConfigWithFingerprint reads config.toml once and returns it with its lock fingerprint.
func loadConfigWithFingerprint(path string) (config.Config, string, error) {
	cfg, contents, err := config.LoadWithContents(path)
	if err != nil {
		return config.Config{}, "", err
	}
	return cfg, lock.Fingerprint(contents), nil
}

// configShell returns the shell named by SHELF_SHELL, erroring on an unsupported value.
func configShell() (config.Shell, error) {
	switch value := os.Getenv("SHELF_SHELL"); value {
	case "":
		return config.Zsh, nil
	case string(config.Bash):
		return config.Bash, nil
	case string(config.Zsh):
		return config.Zsh, nil
	default:
		return "", fmt.Errorf("unsupported shell %q in SHELF_SHELL", value)
	}
}

// resolveShell returns the configured shell when the config sets one, otherwise SHELF_SHELL.
func resolveShell(cfg config.Config) (config.Shell, error) {
	if cfg.Shell != "" {
		return cfg.Shell, nil
	}
	return configShell()
}

func Execute(args []string, stdout, stderr io.Writer) error {
	command := NewRoot()
	command.SetArgs(args)
	command.SetOut(stdout)
	command.SetErr(stderr)
	return command.Execute()
}

// RuntimeContext reports settings to non-Cobra callers, delegating paths to ResolvePaths.
func RuntimeContext() Context {
	context := Context{Profile: profile, Quiet: quiet, NonInteractive: nonInteractive, Verbose: verbose, Color: color}
	paths, err := ResolvePaths(homeDir(), configDir, dataDir, configFile)
	if err != nil {
		// An unresolvable home directory leaves the path fields empty rather than guessing.
		return context
	}
	context.ConfigFile = paths.ConfigFile
	context.ConfigDirectory = paths.ConfigDirectory
	context.DataDirectory = paths.DataDirectory
	return context
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
