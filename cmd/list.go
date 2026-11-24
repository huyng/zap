package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/huyng/zap/internal/scope"
	"github.com/spf13/cobra"
)

var listFlags struct {
	system bool
	root   string
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed apps",
	Long: `List the apps installed in a scope, read from their app.toml files.

A directory without an app.toml is not an app and is not listed: what a removal
leaves behind is data/ alone.`,
	Args: cobra.NoArgs,
	RunE: runList,
}

func init() {
	listCmd.SilenceUsage = true

	flags := listCmd.Flags()
	flags.BoolVar(&listFlags.system, "system", false, "list the system scope (/opt/apps)")
	flags.StringVar(&listFlags.root, "root", "", "app root parent directory (default $APP_HOME or ~/.apps; /opt/apps with --system)")
}

func runList(cmd *cobra.Command, args []string) error {
	sc, err := resolveScope(listFlags.system, listFlags.root)
	if err != nil {
		return err
	}

	entries, err := scope.List(sc)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(entries) == 0 {
		fmt.Fprintf(out, "no apps installed in %s (%s scope)\n", sc.RootDir, sc.Label())
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tVERSION\tKIND\tNAME")
	for _, e := range entries {
		if e.ReadErr != nil {
			fmt.Fprintf(w, "%s\t?\tbroken\t%s\n", e.ID, e.ReadErr)
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.ID, e.Version, kindOf(e), e.Name)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(out, "\n%d app(s) in %s (%s scope)\n", len(entries), sc.RootDir, sc.Label())
	return nil
}

// kindOf describes what an install can do: run its entrypoint, or nothing.
func kindOf(e scope.Entry) string {
	if e.Runnable() {
		return "runnable"
	}
	return "payload"
}
