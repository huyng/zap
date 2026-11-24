package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/huyng/zap/internal/bundle"
	"github.com/huyng/zap/internal/github"
	"github.com/huyng/zap/internal/install"
	"github.com/huyng/zap/internal/scope"
	"github.com/spf13/cobra"
)

var installFlags struct {
	system  bool
	appsDir string
	force   bool
	id      string
	name    string
}

var installCmd = &cobra.Command{
	Use:   "install <archive|github-url>",
	Short: "Install an app bundle",
	Long: `Install an app bundle into its app root and expose its entrypoint.

A conforming bundle is a .tar.gz or .zip whose root holds exactly one <id>/
directory with app.toml and contents/ inside it. It is installed as is.

A GitHub release URL installs the platform asset from that release:
  zap install https://github.com/neovim/neovim/releases/tag/v0.12.2

A GitHub repository URL installs the current head of the default branch, for
tools that cut no releases. The version recorded is the short commit hash:
  zap install https://github.com/huyng/bashmarks

Both GitHub forms are wrapped into a bundle first, then installed the same way
as a bundle archive. The bundle id defaults to io.github.<owner>.<repo>.

For a source tree or a release asset, the identity and entrypoint come from an
app.toml at the tree's root when it declares them. The tree's own id wins over
the derived one, because the author knows the namespace the app lives in and a
renamed repository should not move an installed app root. --id and --name win
over both.

zap never picks an entrypoint itself: a tree with no app.toml, or one that
names no entrypoint, is installed as a payload with nothing exposed.`,
	Args: cobra.ExactArgs(1),
	RunE: runInstall,
}

func init() {
	installCmd.SilenceUsage = true

	flags := installCmd.Flags()
	flags.BoolVar(&installFlags.system, "system", false, "install at system scope (/opt/apps, /usr/local/bin)")
	flags.StringVar(&installFlags.appsDir, "apps-dir", "", "directory holding app roots (default $APP_HOME or ~/.apps; /opt/apps with --system)")
	flags.BoolVar(&installFlags.force, "force", false, "replace an existing install, including its data/")
	flags.StringVar(&installFlags.id, "id", "", "bundle id to use for a GitHub install (default io.github.<owner>.<repo>)")
	flags.StringVar(&installFlags.name, "name", "", "bundle name to use for a GitHub install (default the repo name)")
}

func runInstall(cmd *cobra.Command, args []string) error {
	sc, err := resolveScope(installFlags.system, installFlags.appsDir)
	if err != nil {
		return err
	}

	installer := &install.Installer{
		Scope: sc,
		Force: installFlags.force,
		Tool:  tool(),
		Log:   cmd.OutOrStdout(),
	}

	source := args[0]
	var result *install.Result

	switch {
	case isReleaseURL(source):
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
		defer cancel()
		rel, _ := github.ParseReleaseURL(source)
		result, err = installer.InstallRelease(ctx, httpClient(), rel, builderFor(rel.Owner, rel.Repo, rel.Tag), runtime.GOOS, runtime.GOARCH)
	case isRepoURL(source):
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
		defer cancel()
		repo, _ := github.ParseRepoURL(source)
		src, resolveErr := github.ResolveHead(ctx, httpClient(), repo)
		if resolveErr != nil {
			return resolveErr
		}
		result, err = installer.InstallSource(ctx, httpClient(), src, builderFor(src.Owner, src.Name, ""))
	default:
		if isWebURL(source) {
			return fmt.Errorf("%q is not a GitHub release or repository URL; expected https://github.com/{owner}/{repo} or https://github.com/{owner}/{repo}/releases/tag/{tag}", source)
		}
		result, err = installer.InstallArchive(source)
	}
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "installed %s %s\n  root: %s\n", result.Manifest.ID, result.Manifest.Version, result.Root)
	if result.Receipt != nil && result.Receipt.Source.URL != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  from: %s\n", result.Receipt.Source.URL)
	}
	if result.Exposed != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  link: %s\n", result.Exposed)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "  no entrypoint, nothing exposed; files are under %s/%s/\n",
			result.Root, bundle.ContentsDir)
	}
	return nil
}

// isReleaseURL reports whether s names a single GitHub release.
func isReleaseURL(s string) bool {
	_, err := github.ParseReleaseURL(s)
	return err == nil
}

// isRepoURL reports whether s names a GitHub repository.
func isRepoURL(s string) bool {
	_, err := github.ParseRepoURL(s)
	return err == nil
}

// isWebURL reports whether s looks like a URL rather than a local path.
func isWebURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// httpClient is the client used for API calls and downloads.
func httpClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute}
}

// builderFor picks the bundle identity for a GitHub install. Three sources, in
// increasing authority: the id derived from the repository, the identity the
// tree's own app.toml declares, and an explicit --id or --name. The derived
// default is namespaced to the repository so that repeat installs land on the
// same app root without the tree having to declare anything.
func builderFor(owner, repo, version string) install.Builder {
	return install.Builder{
		ID:           "io.github." + sanitizeID(owner) + "." + sanitizeID(repo),
		Name:         repo,
		Version:      version,
		OverrideID:   installFlags.id,
		OverrideName: installFlags.name,
	}
}

var idUnsafe = regexp.MustCompile(`[^a-z0-9._-]+`)

// sanitizeID folds an owner or repo name into the character set allowed by the
// manifest id rules.
func sanitizeID(s string) string {
	s = idUnsafe.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-.")
}

// resolveScope decides where an install goes, reading the environment once at
// the edge of the program.
//
// The apps directory follows the flag, the system default, or $APP_HOME and the
// home directory in that order. The bin directory follows the scope alone: a
// system install exposes into /usr/local/bin, a user install into ~/.local/bin.
func resolveScope(system bool, appsDir string) (scope.Scope, error) {
	resolved := scope.Scope{System: system}

	switch {
	case appsDir != "":
		resolved.AppsDir = appsDir
	case system:
		resolved.AppsDir = "/opt/apps"
	default:
		base := os.Getenv("APP_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return scope.Scope{}, err
			}
			base = filepath.Join(home, ".apps")
		}
		resolved.AppsDir = base
	}

	if system {
		resolved.BinDir = "/usr/local/bin"
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return scope.Scope{}, err
		}
		resolved.BinDir = filepath.Join(home, ".local", "bin")
	}

	return resolved, nil
}
