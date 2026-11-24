package cmd

import (
	"fmt"
	"runtime"

	"github.com/huyng/zap/internal/pack"
	"github.com/spf13/cobra"
)

var bundleFlags struct {
	out  string
	os   string
	arch string
}

var bundleCmd = &cobra.Command{
	Use:   "bundle <dir>",
	Short: "Pack an app tree into a bundle archive",
	Long: `Pack an app tree into a .tar.gz that installs with "zap install".

The tree is either a bundle root, with app.toml beside contents/:

  build/io.github.huyng.zap/
    app.toml
    contents/bin/zap

or a flat source tree, with app.toml beside the files themselves:

  bashmarks/
    app.toml
    bin/bashmarks.sh

A flat tree is transformed as §6 of the bundle layout describes: the manifest
moves to the archive root and everything else goes under contents/. Which shape
it is decided by one test: a directory holding contents/ is a bundle root, and
anything else is flat.

Either way the archive holds a single <id>/ directory, named from the manifest's
id rather than from the directory you pass. The directory's own name is never
read, so "build/dist" packs exactly the same as "build/io.github.huyng.zap".

The archive is named <id>-<os>-<arch>, with the platform tokens taken from
--os and --arch so that "zap install <release-url>" can find it in a release.
Pass --os "" to leave a token out.

Nothing is written inside <dir>. Archives are reproducible: entries are sorted
and timestamps fixed, so the same tree always packs to the same bytes.

A flat tree ships everything it holds except app.toml. For a compiled project,
build into a clean directory and keep the source tree out of it.`,
	Args: cobra.ExactArgs(1),
	RunE: runBundle,
}

func init() {
	bundleCmd.SilenceUsage = true

	flags := bundleCmd.Flags()
	flags.StringVar(&bundleFlags.out, "out", "", "directory to write the archive to (default the current directory)")
	flags.StringVar(&bundleFlags.os, "os", runtime.GOOS, "platform token in the archive name (empty to leave it out)")
	flags.StringVar(&bundleFlags.arch, "arch", runtime.GOARCH, "architecture token in the archive name (empty to leave it out)")
}

func runBundle(cmd *cobra.Command, args []string) error {
	result, err := pack.Bundle(args[0], pack.Options{
		OutDir: bundleFlags.out,
		OS:     bundleFlags.os,
		Arch:   bundleFlags.arch,
		Log:    cmd.OutOrStdout(),
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "bundled %s %s\n  archive: %s\n", result.Manifest.ID, result.Manifest.Version, result.Path)
	if result.Manifest.Runnable() {
		fmt.Fprintf(out, "  entrypoint: %s\n", result.Manifest.Entrypoint)
	} else {
		fmt.Fprintf(out, "  no entrypoint; installs as a payload\n")
	}
	return nil
}
