package cmd

import (
	"fmt"
	"path/filepath"
	"text/tabwriter"

	"github.com/huyng/zap/internal/bundle"
	"github.com/huyng/zap/internal/scope"
	"github.com/spf13/cobra"
)

var listFlags struct {
	system  bool
	appsDir string
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
	flags.StringVar(&listFlags.appsDir, "apps-dir", "", "directory holding app roots (default $APP_HOME or ~/.apps; /opt/apps with --system)")
}

func runList(cmd *cobra.Command, args []string) error {
	sc, err := resolveScope(listFlags.system, listFlags.appsDir)
	if err != nil {
		return err
	}

	entries, err := scope.List(sc)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(entries) == 0 {
		fmt.Fprintf(out, "no apps installed in %s (%s scope)\n", sc.AppsDir, sc.Label())
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tVERSION\tKIND\tNAME\tORIGIN")
	for _, e := range entries {
		if e.ReadErr != nil {
			fmt.Fprintf(w, "%s\t?\tbroken\t%s\t-\n", e.ID, e.ReadErr)
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", e.ID, e.Version, kindOf(e), e.Name, originOf(e.Root))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(out, "\n%d app(s) in %s (%s scope)\n", len(entries), sc.AppsDir, sc.Label())
	return nil
}

// kindOf describes what an install can do: run its entrypoint, or nothing.
func kindOf(e scope.Entry) string {
	if e.Runnable() {
		return "runnable"
	}
	return "payload"
}

// originOf reads the receipt of an installed app (§6.1) and renders where it
// came from. It returns "-" when there is no receipt, or one that records no
// source: an app can be installed perfectly well without either, so an empty
// column means "unknown", not "broken".
func originOf(root string) string {
	receipt, err := bundle.LoadReceipt(filepath.Join(root, bundle.ReceiptFile))
	if err != nil || receipt == nil || receipt.Source.URL == "" {
		return "-"
	}
	if receipt.Source.Ref != "" && receipt.Source.Kind == "repo" {
		return receipt.Source.URL + "@" + receipt.Source.Ref[:min(len(receipt.Source.Ref), 7)]
	}
	return receipt.Source.URL
}
