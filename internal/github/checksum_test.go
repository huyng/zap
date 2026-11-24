package github

import "testing"

const digest = "9f2c8a1b4e6d0f3a5c7e9b1d2f4a6c8e0b2d4f6a8c0e2b4d6f8a0c2e4b6d8f0a"

func TestParseChecksum(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		asset   string
		want    string
		wantErr bool
	}{
		{
			name:  "lone digest",
			text:  digest + "\n",
			asset: "tool-linux-arm64.tar.gz",
			want:  digest,
		},
		{
			name:  "sha256sum layout",
			text:  digest + "  tool-linux-arm64.tar.gz\n",
			asset: "tool-linux-arm64.tar.gz",
			want:  digest,
		},
		{
			name:  "binary mode marker",
			text:  digest + " *tool-linux-arm64.tar.gz\n",
			asset: "tool-linux-arm64.tar.gz",
			want:  digest,
		},
		{
			name:  "combined file picks the right line",
			text:  "1111111111111111111111111111111111111111111111111111111111111111  other.zip\n" + digest + "  tool-linux-arm64.tar.gz\n",
			asset: "tool-linux-arm64.tar.gz",
			want:  digest,
		},
		{
			name:    "combined file without the asset",
			text:    "1111111111111111111111111111111111111111111111111111111111111111  other.zip\n",
			asset:   "tool-linux-arm64.tar.gz",
			wantErr: true,
		},
		{
			name:  "leading dot slash is ignored",
			text:  digest + "  ./tool-linux-arm64.tar.gz\n",
			asset: "tool-linux-arm64.tar.gz",
			want:  digest,
		},
		{
			name:    "not a digest",
			text:    "<html>404</html>\n",
			asset:   "tool-linux-arm64.tar.gz",
			wantErr: true,
		},
		{
			name: "two lone digests are ambiguous",
			text: digest + "\n" +
				"1111111111111111111111111111111111111111111111111111111111111111\n",
			asset:   "tool-linux-arm64.tar.gz",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseChecksum(tc.text, tc.asset)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseDigestField(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"sha256:" + digest, digest, true},
		{"sha512:" + digest, "", false},
		{"", "", false},
		{"sha256:tooshort", "", false},
	}

	for _, tc := range tests {
		got, ok := parseDigestField(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseDigestField(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFindChecksum(t *testing.T) {
	assets := []Asset{
		{Name: "tool-linux-arm64.tar.gz"},
		{Name: "tool-linux-arm64.tar.gz.sha256sum"},
		{Name: "SHA256SUMS"},
	}

	got, ok := FindChecksum(assets, "tool-linux-arm64.tar.gz")
	if !ok || got.Name != "tool-linux-arm64.tar.gz.sha256sum" {
		t.Fatalf("per-asset sidecar should win, got %q (found=%v)", got.Name, ok)
	}

	got, ok = FindChecksum(assets, "tool-darwin-arm64.tar.gz")
	if !ok || got.Name != "SHA256SUMS" {
		t.Fatalf("combined file should be the fallback, got %q (found=%v)", got.Name, ok)
	}

	if _, ok := FindChecksum([]Asset{{Name: "tool.tar.gz"}}, "tool.tar.gz"); ok {
		t.Fatal("expected no checksum asset")
	}
}
