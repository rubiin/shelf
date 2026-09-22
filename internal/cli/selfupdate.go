package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"shelf/internal/selfupdate"
)

// runUpdate performs the self-update; a variable so tests can script it.
var runUpdate = selfupdate.Update

func newSelfUpdateCommand() *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:   "self-update",
		Short: "Update shelf to the latest release",
		Long: "self-update downloads the latest release archive from GitHub, verifies its " +
			"sha256 against the release's checksums.txt, and replaces the running binary.\n\n" +
			"Installations managed by a package manager (AUR, deb, rpm, apk) should keep " +
			"updating through the package manager instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			diagnostics := cmd.ErrOrStderr()
			if quiet {
				diagnostics = nil
			}
			result, err := runUpdate(cmd.Context(), selfupdate.Options{
				CurrentVersion: Version,
				Force:          force,
				Diagnostics:    diagnostics,
			})
			if err != nil {
				return err
			}
			if !result.Updated {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "shelf %s is up to date\n", result.Next)
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "updated shelf: %s -> %s\n", result.Previous, result.Next)
			return err
		},
	}
	command.Flags().BoolVar(&force, "force", false, "update even from a development build")
	return command
}
