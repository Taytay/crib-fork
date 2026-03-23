package cmd

import "github.com/spf13/cobra"

var suspendCmd = &cobra.Command{
	Use:   "suspend",
	Short: "Stop the workspace container without removing it",
	Long: `Stop the workspace container without removing it.

The container is stopped but preserved. Running 'crib up' or 'crib restart'
will restart the same container and only run resume-flow lifecycle hooks
(postStartCommand, postAttachCommand), making it much faster than a full
'crib up' from scratch.

Note: if you edit devcontainer.json while the container is suspended,
'crib restart' may recreate the container to apply the new configuration.

Use 'crib down' to stop and remove the container instead.`,
	Args: noArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		u := newUI()

		eng, _, store, err := newEngine()
		if err != nil {
			return err
		}

		ws, err := currentWorkspace(store, false)
		if err != nil {
			return err
		}

		u.Dim(versionString())

		if err := eng.Suspend(cmd.Context(), ws); err != nil {
			return err
		}

		u.Success("Suspended " + ws.ID)
		return nil
	},
}
