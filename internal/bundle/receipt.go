package bundle

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// ReceiptFile is the install record at the app root (§6.1). It is written by
// the installer and is never part of a bundle.
const ReceiptFile = "receipt.toml"

// Receipt records how an installation was made. Every field is optional: a
// receipt answers questions rather than conforming to a schema, and readers
// ignore keys they do not know.
type Receipt struct {
	Source    ReceiptSource
	Artifact  ReceiptArtifact
	Installer ReceiptInstaller
}

// ReceiptSource says where the bundle came from.
type ReceiptSource struct {
	// Kind is "release", "repo" or "archive".
	Kind string
	// URL is the release, repository, or local archive it was taken from.
	URL string
	// Ref is the tag or commit that was installed, when there is one.
	Ref string
}

// ReceiptArtifact says what was fetched.
type ReceiptArtifact struct {
	// Name is the file name that was downloaded or read.
	Name string
	// SHA256 is the digest of that file, so a later fetch can be compared.
	SHA256 string
	// FetchedAt is an RFC 3339 timestamp.
	FetchedAt string
}

// ReceiptInstaller says which program made the installation.
type ReceiptInstaller struct {
	Name    string
	Version string
	// At is an RFC 3339 timestamp.
	At string
}

// rawReceipt mirrors receipt.toml for decoding and encoding.
type rawReceipt struct {
	Source    *rawReceiptSource    `toml:"source,omitempty"`
	Artifact  *rawReceiptArtifact  `toml:"artifact,omitempty"`
	Installer *rawReceiptInstaller `toml:"installer,omitempty"`
}

type rawReceiptSource struct {
	Kind string `toml:"kind,omitempty"`
	URL  string `toml:"url,omitempty"`
	Ref  string `toml:"ref,omitempty"`
}

type rawReceiptArtifact struct {
	Name      string `toml:"name,omitempty"`
	SHA256    string `toml:"sha256,omitempty"`
	FetchedAt string `toml:"fetched_at,omitempty"`
}

type rawReceiptInstaller struct {
	Name    string `toml:"name,omitempty"`
	Version string `toml:"version,omitempty"`
	At      string `toml:"at,omitempty"`
}

// Write encodes the receipt as receipt.toml at path.
func (r *Receipt) Write(path string) error {
	raw := rawReceipt{}
	if r.Source != (ReceiptSource{}) {
		raw.Source = &rawReceiptSource{
			Kind: r.Source.Kind,
			URL:  r.Source.URL,
			Ref:  r.Source.Ref,
		}
	}
	if r.Artifact != (ReceiptArtifact{}) {
		raw.Artifact = &rawReceiptArtifact{
			Name:      r.Artifact.Name,
			SHA256:    r.Artifact.SHA256,
			FetchedAt: r.Artifact.FetchedAt,
		}
	}
	if r.Installer != (ReceiptInstaller{}) {
		raw.Installer = &rawReceiptInstaller{
			Name:    r.Installer.Name,
			Version: r.Installer.Version,
			At:      r.Installer.At,
		}
	}

	data, err := toml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", ReceiptFile, err)
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadReceipt reads the receipt at path. A missing file returns nil with no
// error: a receipt is optional, and an app root without one is conforming.
//
// Args:
//
//	path: the path to receipt.toml.
//
// Returns:
//
//	The receipt, nil when the file does not exist, or an error if it cannot be
//	read or parsed.
func LoadReceipt(path string) (*Receipt, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var raw rawReceipt
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ReceiptFile, err)
	}

	r := &Receipt{}
	if raw.Source != nil {
		r.Source = ReceiptSource{Kind: raw.Source.Kind, URL: raw.Source.URL, Ref: raw.Source.Ref}
	}
	if raw.Artifact != nil {
		r.Artifact = ReceiptArtifact{
			Name:      raw.Artifact.Name,
			SHA256:    raw.Artifact.SHA256,
			FetchedAt: raw.Artifact.FetchedAt,
		}
	}
	if raw.Installer != nil {
		r.Installer = ReceiptInstaller{
			Name:    raw.Installer.Name,
			Version: raw.Installer.Version,
			At:      raw.Installer.At,
		}
	}
	return r, nil
}
