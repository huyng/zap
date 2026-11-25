package github

import "testing"

func TestParseReleaseSource(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    Repo
		tag     string
		wantErr bool
	}{
		{
			name: "tagged release",
			url:  "https://github.com/neovim/neovim/releases/tag/v0.12.2",
			want: Repo{Owner: "neovim", Name: "neovim"},
			tag:  "v0.12.2",
		},
		{
			name: "latest release",
			url:  "https://github.com/huyng/zap/releases/latest",
			want: Repo{Owner: "huyng", Name: "zap"},
			tag:  "",
		},
		{
			name: "trailing slash",
			url:  "https://github.com/huyng/zap/releases/latest/",
			want: Repo{Owner: "huyng", Name: "zap"},
			tag:  "",
		},
		{
			name:    "releases index is not a release",
			url:     "https://github.com/huyng/zap/releases",
			wantErr: true,
		},
		{
			name:    "tag without a name",
			url:     "https://github.com/huyng/zap/releases/tag",
			wantErr: true,
		},
		{
			name:    "not a release URL",
			url:     "https://github.com/huyng/zap",
			wantErr: true,
		},
		{
			name:    "not github",
			url:     "https://example.com/a/b/releases/tag/v1",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo, tag, err := ParseReleaseSource(tc.url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %v %q", repo, tag)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo != tc.want || tag != tc.tag {
				t.Fatalf("got %v %q, want %v %q", repo, tag, tc.want, tc.tag)
			}
		})
	}
}

// TestParseReleaseURLRejectsLatest keeps the old strict parser for callers that
// genuinely need a fixed tag.
func TestParseReleaseURLRejectsLatest(t *testing.T) {
	if _, err := ParseReleaseURL("https://github.com/huyng/zap/releases/latest"); err == nil {
		t.Fatal("expected an error for a latest URL")
	}
}
