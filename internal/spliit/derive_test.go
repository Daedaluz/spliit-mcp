package spliit_test

import (
	"testing"

	"github.com/daedaluz/spliit-mcp/internal/spliit"
)

func TestDeriveBaseURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://spliit.app/groups/abc/expenses", "https://spliit.app/api/trpc"},
		{"https://spliit.app/groups/abc", "https://spliit.app/api/trpc"},
		{"http://192.0.2.10:3000/groups/abc/expenses", "http://192.0.2.10:3000/api/trpc"},
		// A self-hosted instance under a subpath must keep that prefix.
		{"https://example.com/spliit/groups/abc/expenses", "https://example.com/spliit/api/trpc"},
		// Not a URL, or no /groups/ segment: caller falls back to the default.
		{"abc123", ""},
		{"https://spliit.app/", ""},
		{"ftp://spliit.app/groups/abc", ""},
	}
	for _, c := range cases {
		if got := spliit.DeriveBaseURL(c.in); got != c.want {
			t.Errorf("DeriveBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHostOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://spliit.app/api/trpc", "spliit.app"},
		{"http://192.0.2.10:3000/api/trpc", "192.0.2.10:3000"},
		{"https://example.com/spliit/api/trpc", "example.com/spliit"},
	}
	for _, c := range cases {
		if got := spliit.HostOf(c.in); got != c.want {
			t.Errorf("HostOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
