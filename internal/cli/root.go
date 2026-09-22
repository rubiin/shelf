package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/term"

	"github.com/spf13/cobra"
	"shelf/internal/config"
	"shelf/internal/filelock"
	"shelf/internal/lock"
	"shelf/internal/render"
	"shelf/internal/source"
	"shelf/internal/tui"
)

// Version is the release version, which main stamps through the linker.
var Version = "dev"

// successMark prefixes successful command results on stdout with a check mark.
const successMark = "✓"

var (
	quiet          bool
	nonInteractive bool
	verbose        bool
	color          string
	configDir      string
	dataDir        string
	configFile     string
	profile        string
	forceUpdate    bool
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
		Use:           "shelf",
		Short:         "Modern, fast, configurable shell plugin manager for both bash and zsh",
		SilenceUsage:  true,
		SilenceErrors: true,
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
			if forceUpdate && !update && !reinstall {
				return fmt.Errorf("--force requires --update or --reinstall")
			}
			mode := lock.ModeNormal
			if update {
				mode = lock.ModeUpdate
			}
			if reinstall {
				mode = lock.ModeReinstall
			}
			return withConfigLock(accessWrite, func(paths Paths) error {
				return lockConfig(paths, mode, lockConcurrency, cmd.ErrOrStderr())
			})
		},
	}
	lockCommand.Flags().BoolVar(&update, "update", false, "update plugin sources")
	lockCommand.Flags().BoolVar(&reinstall, "reinstall", false, "reinstall plugin sources")
	lockCommand.Flags().IntVar(&lockConcurrency, "concurrency", lock.DefaultConcurrency, "maximum concurrent plugin installs")
	lockCommand.Flags().BoolVar(&forceUpdate, "force", false, "update frozen plugins too")
	lockCommand.MarkFlagsMutuallyExclusive("update", "reinstall")
	command.AddCommand(lockCommand)

	var relock, sourceUpdate, sourceReinstall bool
	var sourceConcurrency int
	sourceCommand := &cobra.Command{
		Use:   "source",
		Short: "Generate shell code from locked plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if forceUpdate && !sourceUpdate && !sourceReinstall {
				return fmt.Errorf("--force requires --update or --reinstall")
			}
			mode := lock.ModeNormal
			if sourceUpdate {
				mode = lock.ModeUpdate
			}
			if sourceReinstall {
				mode = lock.ModeReinstall
			}
			paths, err := resolvePaths()
			if err != nil {
				return err
			}
			return sourceConfig(paths, cmd.OutOrStdout(), cmd.ErrOrStderr(), relock || sourceUpdate || sourceReinstall, mode, sourceConcurrency)
		},
	}
	sourceCommand.Flags().BoolVar(&relock, "relock", false, "regenerate lock file")
	sourceCommand.Flags().BoolVar(&sourceUpdate, "update", false, "update plugin sources")
	sourceCommand.Flags().BoolVar(&sourceReinstall, "reinstall", false, "reinstall plugin sources")
	sourceCommand.Flags().IntVar(&sourceConcurrency, "concurrency", lock.DefaultConcurrency, "maximum concurrent plugin installs")
	sourceCommand.Flags().BoolVar(&forceUpdate, "force", false, "update frozen plugins too")
	sourceCommand.MarkFlagsMutuallyExclusive("update", "reinstall")
	command.AddCommand(sourceCommand)
	command.AddCommand(&cobra.Command{
		Use:   "reload",
		Short: "Print shell code that reloads the current shell",
		Long: "reload prints an `exec` of the resolved shell, so evaluating it replaces the " +
			"current shell and re-runs its startup files (which include `eval \"$(shelf " +
			"source)\"`) after a configuration change, without opening a new terminal.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := resolvePaths()
			if err != nil {
				return err
			}
			cfg, _, err := loadConfigWithFingerprint(paths.ConfigFile)
			if err != nil {
				return err
			}
			shell, err := resolveShell(cfg)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "exec %s\n", shell)
			return err
		},
	})
	var updateLock bool
	var updateConcurrency int
	updateCommand := &cobra.Command{
		Use:   "update",
		Short: "Update plugin sources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withConfigLock(accessWrite, func(paths Paths) error {
				if updateLock {
					return lockConfig(paths, lock.ModeUpdate, updateConcurrency, cmd.ErrOrStderr())
				}
				return updateSources(paths, cmd.OutOrStdout(), cmd.ErrOrStderr(), updateConcurrency)
			})
		},
	}
	updateCommand.Flags().BoolVar(&updateLock, "lock", false, "write the refreshed lock file without shell output")
	updateCommand.Flags().IntVar(&updateConcurrency, "concurrency", lock.DefaultConcurrency, "maximum concurrent plugin installs")
	updateCommand.Flags().BoolVar(&forceUpdate, "force", false, "update frozen plugins too")
	command.AddCommand(updateCommand)
	command.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print resolved Shelf paths",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := resolvePaths()
			if err != nil {
				return err
			}
			return printPaths(paths, cmd.OutOrStdout())
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Check installed plugin status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withConfigLock(accessRead, func(paths Paths) error {
				return pluginStatus(paths, cmd.OutOrStdout())
			})
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check Shelf configuration and installation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withConfigLock(accessRead, func(paths Paths) error {
				return doctor(paths, cmd.OutOrStdout())
			})
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "clean",
		Short: "Remove unconfigured installed plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withConfigLock(accessWrite, func(paths Paths) error {
				return cleanPlugins(paths, cmd.OutOrStdout())
			})
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withConfigLock(accessRead, func(paths Paths) error {
				return listPlugins(paths, cmd.OutOrStdout())
			})
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "info NAME",
		Short: "Show a locked plugin's source, revision, files, and size",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConfigLock(accessRead, func(paths Paths) error {
				return pluginInfo(paths, args[0], cmd.OutOrStdout())
			})
		},
	})
	var addGitHub, addGit, addGist, addGitLab, addBitbucket, addCodeberg, addRemote, addLocal, addInline string
	var addOptional bool
	var addRev, addBranch, addTag, addProto, addDir, addFile string
	var addUse, addIgnore, addApply, addBuild, addProfiles, addCloneOpts []string
	var addHooks map[string]string
	var addDepth int
	var addFrozen bool
	addCommand := &cobra.Command{
		Use:   "add NAME",
		Short: "Add a plugin to the configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var depth *int
			if cmd.Flags().Changed("depth") {
				depth = &addDepth
			}
			return withConfigLock(accessWrite, func(paths Paths) error {
				if err := config.Add(paths.ConfigFile, args[0], config.RawPlugin{GitHub: addGitHub, Git: addGit, Gist: addGist, GitLab: addGitLab, Bitbucket: addBitbucket, Codeberg: addCodeberg, Remote: addRemote, Local: addLocal, Optional: addOptional, Inline: addInline, Rev: addRev, Branch: addBranch, Tag: addTag, Proto: addProto, Dir: addDir, File: addFile, Use: addUse, Ignore: addIgnore, Apply: addApply, Build: addBuild, Profiles: addProfiles, Hooks: addHooks, CloneOpts: addCloneOpts, Depth: depth, Frozen: addFrozen}); err != nil {
					return err
				}
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s added: %s\n", writerColors(cmd.OutOrStdout()).success(successMark), args[0])
				return err
			})
		},
	}
	addCommand.Flags().StringVar(&addGitHub, "github", "", "GitHub repository")
	addCommand.Flags().StringVar(&addGit, "git", "", "Git repository")
	addCommand.Flags().StringVar(&addGist, "gist", "", "GitHub Gist")
	addCommand.Flags().StringVar(&addGitLab, "gitlab", "", "GitLab repository")
	addCommand.Flags().StringVar(&addBitbucket, "bitbucket", "", "Bitbucket repository")
	addCommand.Flags().StringVar(&addCodeberg, "codeberg", "", "Codeberg repository")
	addCommand.Flags().StringVar(&addRemote, "remote", "", "remote URL")
	addCommand.Flags().StringVar(&addLocal, "local", "", "local path")
	addCommand.Flags().BoolVar(&addOptional, "optional", false, "skip a missing local plugin")
	addCommand.Flags().StringVar(&addInline, "inline", "", "inline plugin content")
	addCommand.Flags().StringVar(&addRev, "rev", "", "Git revision")
	addCommand.Flags().StringVar(&addBranch, "branch", "", "Git branch")
	addCommand.Flags().StringVar(&addTag, "tag", "", "Git tag")
	addCommand.Flags().StringVar(&addProto, "proto", "", "Git protocol for forge sources: https, git, or ssh")
	addCommand.Flags().StringVar(&addDir, "dir", "", "plugin subdirectory")
	addCommand.Flags().StringVar(&addFile, "file", "", "plugin file")
	addCommand.Flags().StringSliceVar(&addUse, "use", nil, "plugin file glob")
	addCommand.Flags().StringSliceVar(&addIgnore, "ignore", nil, "plugin file globs to exclude from loading")
	addCommand.Flags().StringSliceVar(&addApply, "apply", nil, "template names")
	addCommand.Flags().StringArrayVar(&addBuild, "build", nil, "install-time build commands")
	addCommand.Flags().StringSliceVar(&addProfiles, "profiles", nil, "plugin profiles")
	addCommand.Flags().StringSliceVar(&addCloneOpts, "cloneopts", nil, "extra git clone arguments")
	addCommand.Flags().IntVar(&addDepth, "depth", 0, "git clone depth; 0 clones full history")
	addCommand.Flags().BoolVar(&addFrozen, "frozen", false, "pin the plugin and skip its update")
	addCommand.Flags().StringToStringVar(&addHooks, "hooks", nil, "plugin hooks")
	command.AddCommand(addCommand)

	command.AddCommand(&cobra.Command{
		Use:   "edit",
		Short: "Open the configuration in an editor",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withConfigLock(accessWrite, func(paths Paths) error {
				if err := editConfig(paths); err != nil {
					return err
				}
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s edited: %s\n", writerColors(cmd.OutOrStdout()).success(successMark), paths.ConfigFile)
				return err
			})
		},
	})
	var removeInteractive bool
	removeCommand := &cobra.Command{
		Use:   "remove [NAME]",
		Short: "Remove a plugin from the configuration",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConfigLock(accessWrite, func(paths Paths) error {
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
				if err := config.Remove(paths.ConfigFile, args[0]); err != nil {
					return err
				}
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s removed: %s\n", writerColors(cmd.OutOrStdout()).success(successMark), args[0])
				return err
			})
		},
	}
	removeCommand.Flags().BoolVarP(&removeInteractive, "interactive", "i", false, "select plugins to remove interactively")
	command.AddCommand(removeCommand)
	var initShell string
	initCommand := &cobra.Command{
		Use:   "init",
		Short: "Create a new shell plugin configuration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withConfigLock(accessWrite, func(paths Paths) error {
				return initConfig(cmd, paths, initShell)
			})
		},
	}
	initCommand.Flags().StringVar(&initShell, "shell", "", "shell: bash or zsh")
	command.AddCommand(initCommand)
	command.AddCommand(newSelfUpdateCommand())
	// A non-empty Version makes Cobra install a root --version flag that prints `shelf version <Version>`.
	command.Version = Version
	return command
}

// resolvePaths returns the paths shared by every command.
func resolvePaths() (Paths, error) {
	return ResolvePaths(homeDir(), configDir, dataDir, configFile)
}

// access is the kind of config directory lock a command needs.
type access int

const (
	accessRead access = iota
	accessWrite
)

// withConfigLock resolves paths and holds the config directory lock while run executes.
func withConfigLock(mode access, run func(Paths) error) error {
	paths, err := resolvePaths()
	if err != nil {
		return err
	}
	guard, err := filelock.Acquire(paths.ConfigDirectory, mode == accessWrite, styledLines(os.Stderr, ansiWarningColor))
	if err != nil {
		return err
	}
	defer func() { _ = guard.Release() }()
	return run(paths)
}

// sourceInputs holds the config-file derivations a source run needs.
type sourceInputs struct {
	Config          config.Config
	BaseFingerprint string
	Context         lock.Context
	Shell           string
}

// previousETags reads the previous lock's remote validators, so update and relock runs ask the server to skip bodies that have not changed.
func previousETags(paths Paths) map[string]string {
	locked, err := lock.Read(paths.LockFile(profile))
	if err != nil {
		return nil
	}
	return lock.PluginETags(locked)
}

// buildDiagnostics returns the writer build output streams to, or nil when quiet so the lock discards it.
func buildDiagnostics(writer io.Writer) io.Writer {
	if quiet {
		return nil
	}
	return writer
}

// loadSourceInputs reads and validates the config, resolving the lock context and shell.
func loadSourceInputs(paths Paths, diagnostics io.Writer) (sourceInputs, error) {
	cfg, fingerprint, err := loadConfigWithFingerprint(paths.ConfigFile)
	if err != nil {
		return sourceInputs{}, err
	}
	if err := config.Validate(cfg); err != nil {
		return sourceInputs{}, err
	}
	baseFingerprint := fingerprint
	fingerprint = fingerprintWithRevision(baseFingerprint, paths.RevisionLockFile(profile))
	shell, err := resolveShell(cfg)
	if err != nil {
		return sourceInputs{}, err
	}
	return sourceInputs{
		Config:          cfg,
		BaseFingerprint: baseFingerprint,
		Context:         lock.Context{ConfigFile: paths.ConfigFile, ConfigFingerprint: fingerprint, DataDirectory: paths.DataDirectory, Profile: profile, Shell: string(shell), Templates: render.ResolveTemplates(string(shell), cfg.Templates), PreviousETags: previousETags(paths), Force: forceUpdate, Diagnostics: buildDiagnostics(diagnostics)},
		Shell:           string(shell),
	}, nil
}

// renderScript writes the shell code for a verified lock file; the lock records shell and templates, so rendering needs nothing from the config.
func renderScript(output io.Writer, locked lock.LockedConfig, diagnostics io.Writer) error {
	log := newLogger(diagnostics)
	for _, plugin := range locked.Plugins {
		if plugin.Inline != "" {
			log.verboseStatus("Inlined", log.dim(plugin.Name))
			continue
		}
		log.verboseStatus("Rendered", log.dim(plugin.Name))
	}
	script, err := render.Script(locked, locked.Shell)
	if err != nil {
		return err
	}
	_, err = io.WriteString(output, script)
	return err
}

// fingerprintWithShell hashes the config bytes with the shell override, so a lock taken under a different SHELF_SHELL is stale.
func fingerprintWithShell(contents []byte) string {
	return lock.Fingerprint([]byte(lock.Fingerprint(contents) + "\n" + os.Getenv("SHELF_SHELL")))
}

func fingerprintWithRevision(fingerprint, revisionPath string) string {
	contents, err := os.ReadFile(revisionPath)
	if err != nil {
		return lock.Fingerprint([]byte(fingerprint + "\n"))
	}
	return lock.Fingerprint([]byte(fingerprint + "\n" + lock.Fingerprint(contents)))
}

// unlockedLock reads the lock and verifies it against the config's fingerprint without decoding the config, the shell-startup path.
func unlockedLock(paths Paths, lockPath string) (lock.LockedConfig, bool) {
	contents, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		return lock.LockedConfig{}, false
	}
	locked, err := lock.Read(lockPath)
	if err != nil {
		return lock.LockedConfig{}, false
	}
	read := lock.Context{
		ConfigFile:        paths.ConfigFile,
		ConfigFingerprint: fingerprintWithRevision(fingerprintWithShell(contents), paths.RevisionLockFile(profile)),
		DataDirectory:     paths.DataDirectory,
		Profile:           profile,
		Shell:             locked.Shell,
	}
	return locked, lock.VerifyLocked(locked, read)
}

func lockConfig(paths Paths, mode lock.Mode, concurrency int, diagnostics io.Writer) error {
	cfg, fingerprint, err := loadConfigWithFingerprint(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	log := newLogger(diagnostics)
	if profile != "" && !lock.ProfileMatches(cfg, profile) {
		log.warning("Warning", fmt.Sprintf("profile %q matches no plugins", profile))
	}
	plugins := lock.PluginNames(cfg)
	log.header("Loaded", fmt.Sprintf("%d plugins %s", len(plugins), log.dim(displayPath(paths.ConfigFile))))
	for _, name := range plugins {
		plugin := cfg.Plugins[name]
		switch {
		case !lock.Active(plugin.Profiles, profile):
			log.status("Skipped", log.dim(pluginSource(plugin)))
		case mode == lock.ModeUpdate && !forceUpdate && plugin.Frozen && plugin.Inline == "":
			log.status("Frozen", log.dim(pluginSource(plugin)))
		default:
			log.status("Checked", log.dim(pluginSource(plugin)))
		}
	}
	//  prunes installed sources that the config no longer owns before locking.
	if err := cleanUnownedSources(paths.DataDirectory, cfg, log); err != nil {
		return err
	}
	shell, err := resolveShell(cfg)
	if err != nil {
		return err
	}
	context := lock.Context{ConfigFile: paths.ConfigFile, ConfigFingerprint: fingerprint, DataDirectory: paths.DataDirectory, Profile: profile, Shell: string(shell), Templates: render.ResolveTemplates(string(shell), cfg.Templates), PreviousETags: previousETags(paths), Force: forceUpdate, Diagnostics: buildDiagnostics(diagnostics)}
	cfg, err = applyRevisionManifest(paths, cfg, mode)
	if err != nil {
		return err
	}
	locked, err := lock.BuildWithConcurrency(context, cfg, source.NewInstaller(paths.DataDirectory), mode, concurrency)
	if err != nil {
		return err
	}
	revisionPath := paths.RevisionLockFile(profile)
	if err := lock.WriteRevisionManifest(revisionPath, lock.RevisionManifestFrom(locked)); err != nil {
		return err
	}
	locked.ConfigFingerprint = fingerprintWithRevision(fingerprint, revisionPath)
	lockPath := paths.LockFile(profile)
	if err := lock.Write(lockPath, locked); err != nil {
		return err
	}
	log.header("Locked", fmt.Sprintf("%d plugins %s", len(plugins), log.dim(displayPath(lockPath))))
	return nil
}

func applyRevisionManifest(paths Paths, cfg config.Config, mode lock.Mode) (config.Config, error) {
	if mode == lock.ModeUpdate {
		return cfg, nil
	}
	manifest, err := lock.ReadRevisionManifest(paths.RevisionLockFile(profile))
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return config.Config{}, err
	}
	return lock.ApplyRevisionManifest(cfg, manifest), nil
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

func editConfig(paths Paths) error {
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

// sourceConfig prints shell code, reading a fresh lock file under a shared lock and relocking exclusively only when needed.
func sourceConfig(paths Paths, output, diagnostics io.Writer, force bool, mode lock.Mode, concurrency int) error {
	lockPath := paths.LockFile(profile)
	log := newLogger(diagnostics)
	if !force {
		guard, err := filelock.Acquire(paths.ConfigDirectory, false, styledLines(diagnostics, ansiWarningColor))
		if err != nil {
			return err
		}
		if locked, valid := unlockedLock(paths, lockPath); valid {
			defer func() { _ = guard.Release() }()
			log.verboseHeader("Unlocked", displayPath(lockPath))
			if locked.ProfileMatch == "unmatched" {
				log.warning("Warning", fmt.Sprintf("profile %q matches no plugins", profile))
			}
			if err := lock.Restore(locked, source.NewInstaller(paths.DataDirectory), concurrency); err != nil {
				return err
			}
			return renderScript(output, locked, diagnostics)
		}
		if err := guard.Release(); err != nil {
			return err
		}
	}
	guard, err := filelock.Acquire(paths.ConfigDirectory, true, styledLines(diagnostics, ansiWarningColor))
	if err != nil {
		return err
	}
	defer func() { _ = guard.Release() }()
	// Another process may have edited the config or relocked while we waited for the lock.
	inputs, err := loadSourceInputs(paths, diagnostics)
	if err != nil {
		return err
	}
	if err := cleanUnownedSources(paths.DataDirectory, inputs.Config, log); err != nil {
		return err
	}
	if profile != "" && !lock.ProfileMatches(inputs.Config, profile) {
		log.warning("Warning", fmt.Sprintf("profile %q matches no plugins", profile))
	}
	inputs.Config, err = applyRevisionManifest(paths, inputs.Config, mode)
	if err != nil {
		return err
	}
	locked, err := lock.BuildWithConcurrency(inputs.Context, inputs.Config, source.NewInstaller(paths.DataDirectory), mode, concurrency)
	if err != nil {
		return err
	}
	revisionPath := paths.RevisionLockFile(profile)
	if err := lock.WriteRevisionManifest(revisionPath, lock.RevisionManifestFrom(locked)); err != nil {
		return err
	}
	locked.ConfigFingerprint = fingerprintWithRevision(inputs.BaseFingerprint, revisionPath)
	if err := lock.Write(lockPath, locked); err != nil {
		return err
	}
	return renderScript(output, locked, diagnostics)
}

func updateSources(paths Paths, output, diagnostics io.Writer, concurrency int) error {
	inputs, err := loadSourceInputs(paths, diagnostics)
	if err != nil {
		return err
	}
	log := newLogger(diagnostics)
	// Update skips frozen plugins unless forced, so their status marks them.
	for _, name := range lock.PluginNames(inputs.Config) {
		plugin := inputs.Config.Plugins[name]
		if forceUpdate || !plugin.Frozen || plugin.Inline != "" || !lock.Active(plugin.Profiles, profile) {
			continue
		}
		log.status("Frozen", log.dim(pluginSource(plugin)))
	}
	if err := cleanUnownedSources(paths.DataDirectory, inputs.Config, newLogger(diagnostics)); err != nil {
		return err
	}
	locked, err := lock.BuildWithConcurrency(inputs.Context, inputs.Config, source.NewInstaller(paths.DataDirectory), lock.ModeUpdate, concurrency)
	if err != nil {
		return err
	}
	return renderScript(output, locked, diagnostics)
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
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), writerColors(cmd.ErrOrStderr()).dim("cancelled"))
		return nil
	}
	if err != nil {
		return err
	}
	for _, name := range selection {
		if err := config.Remove(paths.ConfigFile, name); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%s removed: %s\n", writerColors(out).success(successMark), name)
	}
	if len(selection) > 0 {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), writerColors(cmd.ErrOrStderr()).dim("run 'shelf lock' to update the lock file"))
	}
	return nil
}

// initShellPrompt and initConfirmPrompt ask the init questions; variables so tests can script them.
var initShellPrompt = func(in *bufio.Reader, out io.Writer) (config.Shell, error) {
	choice, err := tui.Choose("Select shell:", []string{"bash", "zsh"}, in, out)
	if err != nil {
		return "", err
	}
	return config.Shell(choice), nil
}

var initConfirmPrompt = func(path string, in *bufio.Reader, out io.Writer) (bool, error) {
	return tui.Confirm(fmt.Sprintf("Initialize config at %s?", path), in, out)
}

// initConfig refuses to reinitialize an existing config, otherwise asks which
// shell to configure, confirms the target path, and only then writes the
// config. --non-interactive and the --shell flag skip the prompts; the success
// message is always printed last.
func initConfig(cmd *cobra.Command, paths Paths, flagShell string) error {
	if _, err := os.Stat(paths.ConfigFile); err == nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), writerColors(cmd.ErrOrStderr()).warn("config already exists at "+paths.ConfigFile))
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	shell, err := configShell()
	if err != nil {
		return err
	}
	if flagShell != "" {
		shell = config.Shell(flagShell)
	}
	out := cmd.OutOrStdout()
	if !nonInteractive {
		in := bufio.NewReader(cmd.InOrStdin())
		if flagShell == "" {
			shell, err = initShellPrompt(in, out)
			if err != nil {
				return err
			}
		}
		ok, err := initConfirmPrompt(paths.ConfigFile, in, out)
		if err != nil {
			return err
		}
		if !ok {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), writerColors(cmd.ErrOrStderr()).dim("aborted"))
			return nil
		}
	}
	if err := config.Initialize(paths.ConfigFile, shell); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "%s initialized %s config at %s\n", writerColors(out).success(successMark), shell, paths.ConfigFile)
	return nil
}

func listPlugins(paths Paths, output io.Writer) error {
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return err
	}
	// PluginNames also reports plugins declared with dotted keys, which PluginOrder misses.
	for _, name := range lock.PluginNames(cfg) {
		if _, err := fmt.Fprintln(output, name); err != nil {
			return err
		}
	}
	return nil
}

// pluginInfo reports a locked plugin's source, revision, selected files, and installed size from the lock file.
func pluginInfo(paths Paths, name string, output io.Writer) error {
	locked, err := lock.Read(paths.LockFile(profile))
	if err != nil {
		return err
	}
	var plugin lock.LockedPlugin
	found := false
	for _, candidate := range locked.Plugins {
		if candidate.Name == name {
			plugin, found = candidate, true
			break
		}
	}
	if !found {
		return fmt.Errorf("plugin %q is not in the lock file", name)
	}
	// Local and remote plugins record no source; their installed directory names the source instead.
	source := plugin.Source
	switch {
	case plugin.Inline != "":
		source = "inline"
	case source == "":
		source = plugin.Directory
	}
	var lines [][2]string
	if source != "" {
		lines = append(lines, [2]string{"source", source})
	}
	if plugin.Rev != "" {
		lines = append(lines, [2]string{"rev", plugin.Rev})
	}
	for _, file := range plugin.Files {
		lines = append(lines, [2]string{"files", file})
	}
	// Inline plugins have nothing installed, so they have no size.
	if plugin.Directory != "" {
		total, err := directorySize(plugin.Directory)
		if err != nil {
			return fmt.Errorf("measure plugin %q size: %w", name, err)
		}
		lines = append(lines, [2]string{"size", humanSize(total)})
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(output, "- %s: %q\n", line[0], line[1]); err != nil {
			return err
		}
	}
	return nil
}

// directorySize sums the bytes of every file under a directory.
func directorySize(directory string) (int64, error) {
	var total int64
	err := filepath.WalkDir(directory, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// humanSize formats a byte count the way du does: 512B, 24K, 1.2M.
func humanSize(total int64) string {
	units := []string{"B", "K", "M", "G", "T"}
	value := float64(total)
	index := 0
	for value >= 1024 && index < len(units)-1 {
		value /= 1024
		index++
	}
	if index == 0 {
		return fmt.Sprintf("%.0f%s", value, units[index])
	}
	return fmt.Sprintf("%.1f%s", value, units[index])
}

func printPaths(paths Paths, output io.Writer) error {
	for _, entry := range []struct {
		name string
		path string
	}{
		{"config_dir", paths.ConfigDirectory},
		{"data_dir", paths.DataDirectory},
		{"config_file", paths.ConfigFile},
		{"lock_file", paths.LockFile(profile)},
	} {
		if _, err := fmt.Fprintf(output, "%s=%s\n", entry.name, entry.path); err != nil {
			return err
		}
	}
	return nil
}

func pluginStatus(paths Paths, output io.Writer) error {
	cfg, fingerprint, err := loadConfigWithFingerprint(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	fingerprint = fingerprintWithRevision(fingerprint, paths.RevisionLockFile(profile))
	shell, err := resolveShell(cfg)
	if err != nil {
		return err
	}
	lockPath := paths.LockFile(profile)
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
	// Check every git-sourced plugin's revision in parallel, indexed by position so output stays in declaration order.
	plugins := locked.Plugins
	states := make([]string, len(plugins))
	var tasks []int
	for index, plugin := range plugins {
		if plugin.Rev == "" {
			states[index] = "ok"
			continue
		}
		tasks = append(tasks, index)
	}
	if err := lock.RunConcurrently(len(tasks), lock.DefaultConcurrency, func(_ context.Context, task int) error {
		plugin := plugins[tasks[task]]
		output, err := exec.Command("git", "-C", plugin.Directory, "rev-parse", "HEAD").Output()
		if err != nil {
			states[tasks[task]] = "unable to read revision"
		} else if revision := strings.TrimSpace(string(output)); revision != plugin.Rev {
			states[tasks[task]] = "revision " + revision + ", want " + plugin.Rev
		} else {
			states[tasks[task]] = "ok"
		}
		return nil
	}); err != nil {
		return err
	}
	var unhealthy bool
	outColors := writerColors(output)
	for index, plugin := range plugins {
		state := states[index]
		if state != "ok" {
			unhealthy = true
		}
		var display string
		if state == "ok" {
			display = outColors.success(state)
		} else {
			display = outColors.error(state)
		}
		if _, err := fmt.Fprintf(output, "%s: %s\n", plugin.Name, display); err != nil {
			return err
		}
	}
	if unhealthy {
		return fmt.Errorf("installed plugins differ from the lockfile")
	}
	return nil
}

func doctor(paths Paths, output io.Writer) error {
	cfg, fingerprint, err := loadConfigWithFingerprint(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	fingerprint = fingerprintWithRevision(fingerprint, paths.RevisionLockFile(profile))
	outColors := writerColors(output)
	if _, err := fmt.Fprintf(output, "%s  %s\n", outColors.header(fmt.Sprintf("%-8s", "version:")), "shelf "+Version); err != nil {
		return err
	}
	shell, err := resolveShell(cfg)
	if err != nil {
		return err
	}
	shellPath, err := exec.LookPath(string(shell))
	if err != nil {
		return fmt.Errorf("%s is not installed: %w", shell, err)
	}
	shellVersion, err := shellVersion(shellPath)
	if err != nil {
		return fmt.Errorf("could not determine %s version: %w", shell, err)
	}
	if _, err := fmt.Fprintf(output, "%s  %s\n", outColors.header(fmt.Sprintf("%-8s", "shell:")), displayPath(shellPath)); err != nil {
		return err
	}
	// Continuation line aligned with the value column under "shell:".
	if _, err := fmt.Fprintf(output, "%*s%s\n", 10, "", outColors.dim(shellVersion)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "%s  %s  %s\n", outColors.header(fmt.Sprintf("%-8s", "config:")), outColors.success("ok"), displayPath(paths.ConfigFile)); err != nil {
		return err
	}
	if usesGit(cfg) {
		git, err := exec.LookPath("git")
		if err != nil {
			return fmt.Errorf("git is required for configured Git plugins: %w", err)
		}
		if _, err := fmt.Fprintf(output, "%s  %s  %s\n", outColors.header(fmt.Sprintf("%-8s", "git:")), outColors.success("ok"), displayPath(git)); err != nil {
			return err
		}
	}
	valid, err := lock.Verify(paths.LockFile(profile), lock.Context{ConfigFile: paths.ConfigFile, ConfigFingerprint: fingerprint, DataDirectory: paths.DataDirectory, Profile: profile, Shell: string(shell)})
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("lockfile is stale or selected plugin files are missing")
	}
	if _, err := fmt.Fprintf(output, "%s  %s  %s\n", outColors.header(fmt.Sprintf("%-8s", "lock:")), outColors.success("ok"), displayPath(paths.LockFile(profile))); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "\n"+outColors.success("No problems found"))
	return err
}

// shellVersion returns the first line of `<shell> --version`.
func shellVersion(shellPath string) (string, error) {
	output, err := exec.Command(shellPath, "--version").Output()
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	return strings.TrimSpace(line), nil
}

func usesGit(cfg config.Config) bool {
	for _, plugin := range cfg.Plugins {
		if plugin.Git != "" || plugin.GitHub != "" || plugin.Gist != "" {
			return true
		}
	}
	return false
}

func cleanPlugins(paths Paths, output io.Writer) error {
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}
	removed, err := cleanInstallDirectories(paths.DataDirectory, cfg)
	if err != nil {
		return err
	}
	colors := writerColors(output)
	if len(removed) == 0 {
		_, err := fmt.Fprintf(output, "%s nothing to clean\n", colors.success(successMark))
		return err
	}
	for _, path := range removed {
		if _, err := fmt.Fprintf(output, "removed: %s\n", installDisplayPath(paths.DataDirectory, path)); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(output, "%s cleaned: %d paths\n", colors.success(successMark), len(removed))
	return err
}

// cleanUnownedSources prunes installed sources the config no longer owns, before locking.
func cleanUnownedSources(dataDirectory string, cfg config.Config, log logger) error {
	removed, err := cleanInstallDirectories(dataDirectory, cfg)
	if err != nil {
		return err
	}
	for _, path := range removed {
		log.verboseWarning("Removed", log.dim(installDisplayPath(dataDirectory, path)))
	}
	return nil
}

// cleanInstallDirectories removes install paths the config no longer owns and names what it removed.
func cleanInstallDirectories(dataDirectory string, cfg config.Config) ([]string, error) {
	kept, sources, err := ownedInstallPaths(dataDirectory, cfg)
	if err != nil {
		return nil, err
	}
	// Inline plugins live in the lock, so nothing owns the plugins directory any more.
	roots := []string{source.CloneDir(dataDirectory), source.DownloadDir(dataDirectory), filepath.Join(dataDirectory, "plugins")}
	var removed []string
	for _, root := range roots {
		paths, err := removeUnownedPaths(root, kept, sources)
		removed = append(removed, paths...)
		if err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// ownedInstallPaths collects the paths the config owns plus the source directories not to walk into.
func ownedInstallPaths(dataDirectory string, cfg config.Config) (map[string]bool, map[string]bool, error) {
	kept := map[string]bool{}
	sources := map[string]bool{}
	for _, root := range []string{source.CloneDir(dataDirectory), source.DownloadDir(dataDirectory)} {
		kept[root] = true
	}
	for _, plugin := range cfg.Plugins {
		switch {
		case plugin.Git != "" || plugin.GitHub != "" || plugin.Gist != "" || plugin.GitLab != "" || plugin.Bitbucket != "" || plugin.Codeberg != "":
			directory, err := source.GitDirectory(dataDirectory, source.Request{Git: plugin.Git, GitHub: plugin.GitHub, Gist: plugin.Gist, GitLab: plugin.GitLab, Bitbucket: plugin.Bitbucket, Codeberg: plugin.Codeberg, Proto: plugin.Proto, Ref: plugin.Rev, Branch: plugin.Branch, Tag: plugin.Tag, Dir: plugin.Dir})
			if err != nil {
				return nil, nil, err
			}
			sources[directory] = true
			keepAncestors(kept, directory)
		case plugin.Remote != "":
			directory, file, err := source.RemoteDirectory(dataDirectory, plugin.Remote)
			if err != nil {
				return nil, nil, err
			}
			kept[file] = true
			keepAncestors(kept, directory)
		}
	}
	return kept, sources, nil
}

// keepAncestors marks a path and every parent directory as owned.
func keepAncestors(kept map[string]bool, path string) {
	for path != "" {
		kept[path] = true
		parent := filepath.Dir(path)
		if parent == path {
			return
		}
		path = parent
	}
}

// removeUnownedPaths deletes everything under root that the config does not own.
func removeUnownedPaths(root string, kept, sources map[string]bool) ([]string, error) {
	var removed []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if path == root {
			return nil
		}
		if kept[path] {
			if entry.IsDir() && sources[path] {
				return fs.SkipDir
			}
			return nil
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		removed = append(removed, path)
		if entry.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	return removed, err
}

// installDisplayPath names an install path relative to the data directory.
func installDisplayPath(dataDirectory, path string) string {
	relative, err := filepath.Rel(dataDirectory, path)
	if err != nil {
		return path
	}
	return relative
}

// loadConfigWithFingerprint reads the config once and returns it with its lock fingerprint.
func loadConfigWithFingerprint(path string) (config.Config, string, error) {
	cfg, contents, err := config.LoadWithContents(path)
	if err != nil {
		return config.Config{}, "", err
	}
	return cfg, fingerprintWithShell(contents), nil
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
	err := command.Execute()
	if err != nil {
		writeError(stderr, err)
	}
	return err
}

// writeError prints a failure as a blank line followed by an error prefix.
func writeError(diagnostics io.Writer, err error) {
	_, _ = fmt.Fprintf(diagnostics, "\n%s %s\n", writerColors(diagnostics).error("error:"), err)
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
