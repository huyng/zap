package cmd

import (
	"os"
	"runtime/debug"

	"github.com/huyng/zap/internal/install"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "zap",
	Short: "zap is a cross-platform binary package manager",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// tool returns the identity recorded in receipt.toml (§6.1). The version comes
// from the module build info, so `go install` of a tagged release reports the
// tag and a working copy reports "dev".
func tool() install.Tool {
	version := "dev"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	return install.Tool{Name: "zap", Version: version}
}

func init() {
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(bundleCmd)
}
