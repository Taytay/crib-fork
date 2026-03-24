package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var restartForceFlag bool

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the workspace container",
	Long: `Restart the workspace container, picking up safe config changes.

If volumes, mounts, ports, environment variables, or other container runtime
settings changed in devcontainer.json (or docker-compose files), the container
is automatically recreated with the new configuration. Only the resume-flow
lifecycle hooks (postStartCommand, postAttachCommand) run — creation hooks
are skipped, making restart much faster than a full rebuild.

When the container is recreated, any state not stored in volumes is lost (for
example, packages installed manually inside the container). Use --force to skip
the confirmation prompt.

If image-affecting changes are detected (image, Dockerfile, features, build
args), restart will ask you to run 'crib rebuild' instead.`,
	Args: noArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		u := newUI()

		eng, d, store, err := newEngine()
		if err != nil {
			return err
		}
		eng.SetOutput(os.Stdout, os.Stderr)
		eng.SetVerbose(verboseFlag || debugFlag)
		eng.SetProgress(func(msg string) { u.Dim("  " + msg) })
		setupPlugins(eng, d)

		ws, err := currentWorkspace(store, false)
		if err != nil {
			return err
		}

		u.Dim(versionString())

		// Check whether restart will recreate the container and warn.
		if !restartForceFlag {
			plan, err := eng.PlanRestart(cmd.Context(), ws)
			if err != nil {
				return err
			}
			if plan.WillRecreate {
				fmt.Fprintln(os.Stderr, "Config changes detected. The container will be recreated and any")
				fmt.Fprintln(os.Stderr, "state not stored in volumes will be lost.")
				confirmed, err := confirmPrompt("recreation requires confirmation")
				if err != nil {
					return err
				}
				if !confirmed {
					u.Dim("Aborted")
					return nil
				}
			}
		}

		u.Header("Restarting workspace")

		result, err := eng.Restart(cmd.Context(), ws)
		if err != nil {
			if result != nil {
				// Container is usable despite hook failure.
				u.Keyval("container", "crib-"+ws.ID)
				u.Keyval("workspace", result.WorkspaceFolder)
			}
			return err
		}

		if result.Recreated {
			u.Success("Workspace recreated")
		} else {
			u.Success("Workspace restarted")
		}
		u.Keyval("container", "crib-"+ws.ID)
		u.Keyval("workspace", result.WorkspaceFolder)
		if result.RemoteUser != "" {
			u.Keyval("user", result.RemoteUser)
		}
		if ports := formatPorts(result.Ports); ports != "" {
			u.Keyval("ports", ports)
		}

		return nil
	},
}

func init() {
	restartCmd.Flags().BoolVarP(&restartForceFlag, "force", "f", false, "skip confirmation prompt when recreating")
}
