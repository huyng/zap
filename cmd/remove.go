package cmd

import (
	"fmt"

	"github.com/huyng/zap/internal/install"
	"github.com/spf13/cobra"
)

var removeFlags struct {
	system bool
	root   string
	purge  bool
	force  bool
}

var removeCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Uninstall an app",
	Long: `Uninstall an app by id.

contents/, cache/, run/ and app.toml are removed, along with the exposed
entrypoint. data/ is kept: a later install of the same id finds its data again.
Pass --purge to delete data/ as well.`,
	Args: cobra.ExactArgs(1),
	RunE: runRemove,
}

func init() {
	removeCmd.SilenceUsage = true

	flags := removeCmd.Flags()
	flags.BoolVar(&removeFlags.system, "system", false, "act on the system scope (/opt/apps, /usr/local/bin)")
	flags.StringVar(&removeFlags.root, "root", "", "app root parent directory (default $APP_HOME or ~/.apps; /opt/apps with --system)")
	flags.BoolVar(&removeFlags.purge, "purge", false, "also delete data/, losing user data")
	flags.BoolVar(&removeFlags.force, "force", false, "remove the directory even if it holds no readable app.toml")
}

func runRemove(cmd *cobra.Command, args []string) error {
	sc, err := resolveScope(removeFlags.system, removeFlags.root)
	if err != nil {
		return err
	}

	remover := &install.Remover{
		Scope: sc,
		Purge: removeFlags.purge,
		Force: removeFlags.force,
		Log:   cmd.OutOrStdout(),
	}

	result, err := remover.Remove(args[0])
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n  root: %s\n", result.ID, result.Root)
	if result.Unlinked != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  unlinked: %s\n", result.Unlinked)
	}
	if result.KeptData != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  kept: %s (pass --purge to remove it)\n", result.KeptData)
	}
	return nil
}
