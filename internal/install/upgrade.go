package install

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/huyng/zap/internal/bundle"
	"github.com/huyng/zap/internal/github"
	"github.com/huyng/zap/internal/scope"
)

// Upgrader updates an installed app by re-fetching whatever its receipt says it
// came from. Like Installer, all of its inputs are fields.
type Upgrader struct {
	Scope scope.Scope
	// Tool names the installer in the receipt the upgrade writes.
	Tool Tool
	// Check resolves and compares without installing anything.
	Check bool
	// Log receives progress messages; nil discards them.
	Log io.Writer
}

// UpgradeResult describes an upgrade, whether or not it was carried out.
type UpgradeResult struct {
	// ID is the app that was examined.
	ID string
	// From is the installed version, To the version the source offers.
	From string
	To   string
	// Ref is the tag or commit the new version came from, when there is one.
	Ref string
	// Changed reports whether the source offers something different from what
	// is installed.
	Changed bool
	// Installed is true when the new tree was actually put in place.
	Installed bool
	// Root and Exposed describe the installation, when one was made.
	Root    string
	Exposed string
}

// Upgrade brings one installed app up to date with the source its receipt
// records (§7). It never touches data/, cache/, or run/, and it refuses to
// replace an installation whose id differs from the one it fetched.
//
// Version strings are opaque (§2.5), so "different" means the source now offers
// a different version or a different artifact digest, not "newer". A source
// that moved backwards is installed like any other change; the caller decides
// whether that is a downgrade worth reporting.
//
// Args:
//
//	ctx: request context for the API and download.
//	client: HTTP client to use.
//	id: the app id, which is also the directory name under the scope root.
//	goos, goarch: platform used to choose a release asset.
//
// Returns:
//
//	What the source offered and what was done, or an error when the app is not
//	installed, has no receipt, or its source cannot be resolved.
func (u *Upgrader) Upgrade(ctx context.Context, client *http.Client, id, goos, goarch string) (*UpgradeResult, error) {
	root := u.Scope.Root(id)

	installed, err := isAppRoot(root)
	if err != nil {
		return nil, err
	}
	if !installed {
		return nil, fmt.Errorf("%s is not installed at %s", id, root)
	}

	current, err := bundle.Load(filepath.Join(root, bundle.ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("reading the installed %s: %w", bundle.ManifestFile, err)
	}

	receipt, err := bundle.LoadReceipt(filepath.Join(root, bundle.ReceiptFile))
	if err != nil {
		return nil, err
	}
	if receipt == nil || receipt.Source.Kind == "" {
		return nil, fmt.Errorf("%s was installed without a receipt, so there is no source to update from; reinstall it to record one", id)
	}

	result := &UpgradeResult{ID: id, From: current.Version}

	f, err := u.fetch(ctx, client, receipt, id, goos, goarch)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// §7.2: an upgrade whose manifest names a different app is not this app's
	// upgrade, however it arrived.
	if f.manifest.ID != id {
		return nil, fmt.Errorf("the source now builds %q, but %q is installed; refusing to replace it", f.manifest.ID, id)
	}

	result.To = f.manifest.Version
	result.Ref = f.receipt.Source.Ref
	result.Changed = f.manifest.Version != current.Version ||
		f.receipt.Artifact.SHA256 != receipt.Artifact.SHA256

	if !result.Changed || u.Check {
		return result, nil
	}

	i := &Installer{Scope: u.Scope, Tool: u.Tool, Log: u.Log}
	promoted, err := i.promote(f, promoteUpgrade)
	if err != nil {
		return nil, err
	}

	result.Installed = true
	result.Root = promoted.Root
	result.Exposed = promoted.Exposed
	return result, nil
}

// fetch rebuilds the bundle from whatever the receipt says the installation
// came from.
func (u *Upgrader) fetch(ctx context.Context, client *http.Client, receipt *bundle.Receipt, id, goos, goarch string) (*fetched, error) {
	i := &Installer{Scope: u.Scope, Tool: u.Tool, Log: u.Log}

	switch receipt.Source.Kind {
	case "archive":
		if _, err := os.Stat(receipt.Source.URL); err != nil {
			return nil, fmt.Errorf("the archive this was installed from is gone: %w", err)
		}
		return i.fetchArchive(receipt.Source.URL)

	case "release":
		rel, err := github.ParseReleaseURL(receipt.Source.URL)
		if err != nil {
			return nil, fmt.Errorf("reading the recorded release URL %q: %w", receipt.Source.URL, err)
		}
		repo := github.Repo{Owner: rel.Owner, Name: rel.Repo}

		latest, err := github.LatestRelease(ctx, client, repo)
		if err != nil {
			return nil, err
		}
		b := Builder{ID: id, Name: rel.Repo, Version: latest.Tag}
		return i.fetchRelease(ctx, client, latest, b, goos, goarch)

	case "repo":
		repo, err := github.ParseRepoURL(receipt.Source.URL)
		if err != nil {
			return nil, fmt.Errorf("reading the recorded repository URL %q: %w", receipt.Source.URL, err)
		}
		src, err := github.ResolveHead(ctx, client, repo)
		if err != nil {
			return nil, err
		}
		b := Builder{ID: id, Name: repo.Name}
		return i.fetchSource(ctx, client, src, b)

	default:
		return nil, fmt.Errorf("unknown source kind %q in %s", receipt.Source.Kind, bundle.ReceiptFile)
	}
}
