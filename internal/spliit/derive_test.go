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

func TestWebURL(t *testing.T) {
	cases := []struct{ base, id, want string }{
		{"https://spliit.app/api/trpc", "abc", "https://spliit.app/groups/abc"},
		{"https://split.inits.se/api/trpc", "abc", "https://split.inits.se/groups/abc"},
		// A subpath install keeps its prefix.
		{"https://example.com/spliit/api/trpc", "abc", "https://example.com/spliit/groups/abc"},
		// Round-trips with DeriveBaseURL.
		{"http://192.0.2.10:3000/api/trpc", "abc", "http://192.0.2.10:3000/groups/abc"},
		{"", "abc", ""},
		{"https://spliit.app/api/trpc", "", ""},
	}
	for _, c := range cases {
		if got := spliit.WebURL(c.base, c.id); got != c.want {
			t.Errorf("WebURL(%q, %q) = %q, want %q", c.base, c.id, got, c.want)
		}
	}
}

// The two must be inverses, or a link derived from a stored group would not
// reach the group it came from.
func TestWebURLRoundTripsWithDeriveBaseURL(t *testing.T) {
	for _, base := range []string{
		"https://spliit.app/api/trpc",
		"https://example.com/spliit/api/trpc",
		"http://192.0.2.10:3000/api/trpc",
	} {
		link := spliit.WebURL(base, "grp-1")
		if got := spliit.DeriveBaseURL(link); got != base {
			t.Errorf("DeriveBaseURL(WebURL(%q)) = %q, want the original", base, got)
		}
	}
}
