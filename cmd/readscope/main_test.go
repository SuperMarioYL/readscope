package main

import "testing"

// The documented go install path cannot set -ldflags, so the reported version
// must fall back to the module version instead of the "dev" sentinel.
func TestResolveVersion(t *testing.T) {
	cases := []struct {
		linked, build, want string
	}{
		{"0.2.0", "v0.2.0", "0.2.0"},
		{"1.0.0", "(devel)", "1.0.0"},
		{"dev", "v0.2.0", "0.2.0"},
		{"dev", "(devel)", "dev"},
		{"dev", "", "dev"},
		{"", "v0.3.1", "0.3.1"},
		{"dev", "v0.1.1-0.20261015002254-74ae069274fb+dirty", "dev"},
	}
	for _, c := range cases {
		if got := resolveVersion(c.linked, c.build); got != c.want {
			t.Errorf("resolveVersion(%q, %q) = %q, want %q", c.linked, c.build, got, c.want)
		}
	}
}

func TestReportedVersionIsNeverEmpty(t *testing.T) {
	if got := reportedVersion(); got == "" {
		t.Fatal("reported version must never be empty")
	}
}
