package cmd

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/huyng/zap/internal/install"
	"github.com/huyng/zap/internal/scope"
	"github.com/spf13/cobra"
)

var updateFlags struct {
	system  bool
	appsDir string
	all     bool
	check   bool
}

var updateCmd = &cobra.Command{
	Use:   "update [<id>...]",
	Short: "Update installed apps from the source they came from",
	Long: `Update installed apps by re-fetching whatever their receipt records.

Every install writes receipt.toml beside app.toml, naming the release, the
repository, or the archive it came from. update reads that record and fetches
again:

  zap update io.github.huyng.bashmarks
  zap update --all
  zap update --check --all

The new tree replaces contents/ and app.toml, and data/, cache/ and run/ are
left alone, so settings survive the upgrade. The app id must be unchanged: a
source that now builds a different id is refused rather than installed over
the existing app.

Version strings carry no ordering (§2.5), so update reports a source that
offers something different rather than something newer. --check resolves and
compares without installing anything.

An app installed without a receipt, or one installed from an archive that has
since been deleted, cannot be updated: reinstall it to record where it came
from.`,
	Args: cobra.ArbitraryArgs,
	RunE: runUpdate,
}

func init() {
	updateCmd.SilenceUsage = true

	flags := updateCmd.Flags()
	flags.BoolVar(&updateFlags.system, "system", false, "act on the system scope (/opt/apps, /usr/local/bin)")
	flags.StringVar(&updateFlags.appsDir, "apps-dir", "", "directory holding app roots (default $APP_HOME or ~/.apps; /opt/apps with --system)")
	flags.BoolVar(&updateFlags.all, "all", false, "update every app in the scope")
	flags.BoolVar(&updateFlags.check, "check", false, "report what would change without installing it")
}

func runUpdate(cmd *cobra.Command, args []string) error {
	if updateFlags.all && len(args) > 0 {
		return fmt.Errorf("name apps to update, or pass --all, but not both")
	}
	if !updateFlags.all && len(args) == 0 {
		return fmt.Errorf("name at least one app to update, or pass --all")
	}

	sc, err := resolveScope(updateFlags.system, updateFlags.appsDir)
	if err != nil {
		return err
	}

	ids := args
	if updateFlags.all {
		entries, err := scope.List(sc)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.ReadErr != nil {
				continue
			}
			ids = append(ids, e.ID)
		}
		if len(ids) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "no apps installed in %s (%s scope)\n", sc.AppsDir, sc.Label())
			return nil
		}
	}

	upgrader := &install.Upgrader{
		Scope: sc,
		Tool:  tool(),
		Check: updateFlags.check,
		Log:   cmd.OutOrStdout(),
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
	defer cancel()

	out := cmd.OutOrStdout()
	failures := 0
	changed := 0

	for _, id := range ids {
		result, err := upgrader.Upgrade(ctx, httpClient(), id, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			// One broken app should not hide the state of the rest. The error
			// names the app itself, so it is printed as it stands.
			failures++
			fmt.Fprintln(out, err)
			continue
		}

		switch {
		case !result.Changed:
			fmt.Fprintf(out, "%s %s is up to date\n", result.ID, result.From)
		case result.Installed:
			changed++
			fmt.Fprintf(out, "updated %s %s -> %s\n  root: %s\n", result.ID, result.From, result.To, result.Root)
			if result.Exposed != "" {
				fmt.Fprintf(out, "  link: %s\n", result.Exposed)
			}
		default:
			changed++
			fmt.Fprintf(out, "%s would update %s -> %s\n", result.ID, result.From, result.To)
		}
	}

	if updateFlags.check && changed > 0 {
		fmt.Fprintf(out, "\n%d app(s) could be updated; run again without --check to install\n", changed)
	}
	if failures > 0 {
		return fmt.Errorf("%d app(s) could not be updated", failures)
	}
	return nil
}
