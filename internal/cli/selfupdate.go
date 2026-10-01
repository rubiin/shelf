package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"shelf/internal/selfupdate"
)

// runUpdate performs the self-update; a variable so tests can script it.
var runUpdate = selfupdate.Update

func newSelfUpdateCommand() *cobra.Command {
	var force, yes bool
	var version string
	command := &cobra.Command{
		Use:   "self-update",
		Short: "Update shelf to the latest release",
		Long: "self-update downloads a release archive from GitHub, verifies its sha256 against " +
			"the release's checksums.txt, and replaces the running binary.\n\n" +
			"--version installs an exact tag, and --yes confirms without a prompt.\n\n" +
			"A package manager that owns the install can disable self-update with a marker " +
			"file, an update-instructions file, or SHELF_SELF_UPDATE_AVAILABLE=false; then " +
			"shelf refuses to replace the packager's binary. --force overrides the refusal.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			diagnostics := cmd.ErrOrStderr()
			if quiet {
				diagnostics = nil
			}
			stop := startProgressSpinner(diagnostics, "Downloading shelf")
			result, err := runUpdate(cmd.Context(), selfupdate.Options{
				CurrentVersion: Version,
				Version:        version,
				Force:          force,
				Yes:            yes,
				Confirm: func(version string) (bool, error) {
					// The prompt needs the terminal to itself, so pause the spinner.
					stop()
					approved, confirmErr := confirmSelfUpdate(cmd, version)
					stop = startProgressSpinner(diagnostics, "Downloading shelf")
					return approved, confirmErr
				},
				Diagnostics: styledLines(diagnostics, ansiStatusColor),
			})
			stop()
			if err != nil {
				return err
			}
			if !result.Updated {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s shelf %s is up to date\n", writerColors(cmd.OutOrStdout()).success(successMark), result.Next)
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s updated shelf: %s -> %s\n", writerColors(cmd.OutOrStdout()).success(successMark), result.Previous, result.Next)
			return err
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	command.Flags().StringVar(&version, "version", "", "install a specific release tag instead of the newest")
	command.Flags().BoolVar(&force, "force", false, "update even from a development build, an already-current release, or an install that disables self-update")
	return command
}

// confirmSelfUpdate asks for approval before the binary is replaced. Without a
// terminal to prompt on it reports no approval, which turns into the --yes hint
// rather than a prompt reading from a pipe.
func confirmSelfUpdate(cmd *cobra.Command, version string) (bool, error) {
	input := cmd.InOrStdin()
	if nonInteractive || !terminalInput(input) {
		return false, nil
	}
	return promptSelfUpdate(input, cmd.ErrOrStderr(), version)
}

// promptSelfUpdate writes the prompt and reads one answer.
func promptSelfUpdate(input io.Reader, prompt io.Writer, version string) (bool, error) {
	if _, err := fmt.Fprintf(prompt, "Update shelf to %s? [y/N] ", version); err != nil {
		return false, err
	}
	answer, err := bufio.NewReader(input).ReadString('\n')
	// A final answer without a newline is still an answer; only a real read
	// failure is an error.
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return approvedAnswer(answer), nil
}

// approvedAnswer reports whether a prompt answer means yes; anything else,
// including an empty line, declines.
func approvedAnswer(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

// terminalInput reports whether a reader can be prompted on.
func terminalInput(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}
