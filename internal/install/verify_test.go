package install

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huyng/zap/internal/github"
)

const (
	goodDigest = "9f2c8a1b4e6d0f3a5c7e9b1d2f4a6c8e0b2d4f6a8c0e2b4d6f8a0c2e4b6d8f0a"
	badDigest  = "1111111111111111111111111111111111111111111111111111111111111111"
)

// serveChecksum stands in for a release's checksum file.
func serveChecksum(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVerify(t *testing.T) {
	asset := github.Asset{Name: "tool-linux-arm64.tar.gz"}

	tests := []struct {
		name    string
		body    string
		digest  string
		noAsset bool
		wantErr bool
	}{
		{
			name:   "published digest matches",
			body:   goodDigest + "  tool-linux-arm64.tar.gz\n",
			digest: goodDigest,
		},
		{
			name:    "published digest differs",
			body:    badDigest + "  tool-linux-arm64.tar.gz\n",
			digest:  goodDigest,
			wantErr: true,
		},
		{
			name:   "digest field wins over the sidecar",
			body:   badDigest + "  tool-linux-arm64.tar.gz\n",
			digest: goodDigest,
		},
		{
			name:    "no checksum published is not a failure",
			noAsset: true,
			digest:  goodDigest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var log bytes.Buffer
			installer := &Installer{Log: &log}

			a := asset
			assets := []github.Asset{asset}

			switch tc.name {
			case "digest field wins over the sidecar":
				// An API digest means the sidecar is never fetched.
				a.Digest = "sha256:" + tc.digest
			case "no checksum published is not a failure":
			default:
				srv := serveChecksum(t, tc.body)
				assets = []github.Asset{asset, {Name: asset.Name + ".sha256sum", URL: srv.URL}}
			}

			err := installer.verify(context.Background(), http.DefaultClient, a, assets, tc.digest)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected a mismatch to be fatal")
				}
				if !strings.Contains(err.Error(), "does not match the checksum") {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.noAsset && !strings.Contains(log.String(), "installing unverified") {
				t.Fatalf("expected an unverified warning, got %q", log.String())
			}
			if !tc.noAsset && !strings.Contains(log.String(), "verified") {
				t.Fatalf("expected a verification line, got %q", log.String())
			}
		})
	}
}
