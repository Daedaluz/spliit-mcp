package oidc

import (
	"errors"
	"testing"
	"time"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
)

// A verified token must not be re-checked with the provider on every request:
// introspection is a network round-trip, and doing it per call is both slow and
// a standing chance for a transient failure to look like a rejected token.
func TestVerifyCacheReturnsWithoutReverifying(t *testing.T) {
	cache := newVerifyCache()
	info := &mcpauth.TokenInfo{
		UserID:     "alice",
		Expiration: time.Now().Add(time.Hour),
	}

	if _, ok := cache.get("token"); ok {
		t.Fatal("empty cache returned a hit")
	}

	cache.put("token", info)

	got, ok := cache.get("token")
	if !ok {
		t.Fatal("cache missed a token just stored")
	}
	if got.UserID != "alice" {
		t.Errorf("UserID = %q, want alice", got.UserID)
	}

	// A different token must not collide with it.
	if _, ok := cache.get("another"); ok {
		t.Error("a different token hit the cache")
	}
}

// Caching must never outlive the token, or a revoked or expired credential
// would keep working.
func TestVerifyCacheNeverOutlivesTheToken(t *testing.T) {
	cache := newVerifyCache()

	// Expires sooner than the cap: the cache must honour the token.
	soon := time.Now().Add(50 * time.Millisecond)
	cache.put("short", &mcpauth.TokenInfo{UserID: "alice", Expiration: soon})

	if _, ok := cache.get("short"); !ok {
		t.Fatal("token should be cached while still valid")
	}
	time.Sleep(80 * time.Millisecond)
	if _, ok := cache.get("short"); ok {
		t.Error("cache served a token past its expiry")
	}

	// Already expired: never cache it at all.
	cache.put("stale", &mcpauth.TokenInfo{
		UserID:     "alice",
		Expiration: time.Now().Add(-time.Minute),
	})
	if _, ok := cache.get("stale"); ok {
		t.Error("an already-expired token was cached")
	}
}

// A token valid for longer than the cap must still be re-checked at the cap, so
// a revocation is noticed reasonably soon.
func TestVerifyCacheCapsLongLivedTokens(t *testing.T) {
	cache := newVerifyCache()
	cache.put("long", &mcpauth.TokenInfo{
		UserID:     "alice",
		Expiration: time.Now().Add(24 * time.Hour),
	})

	key := cacheKeyFor("long")
	entry := cache.entries[key]
	if entry.expires.After(time.Now().Add(maxVerifyCacheTTL + time.Second)) {
		t.Errorf("cache entry outlives the cap: expires in %v", time.Until(entry.expires))
	}
}

// The distinction that matters: ErrInvalidToken becomes a 401, which tells the
// client to send the user through a fresh login. A failure to reach the provider
// must not do that — it is our fault, not the credential's.
func TestTransientFailuresAreNotInvalidToken(t *testing.T) {
	transient := errors.New("introspection request failed: connection refused")
	if errors.Is(transient, mcpauth.ErrInvalidToken) {
		t.Error("a transient upstream failure must not read as an invalid token")
	}

	rejected := errors.New("token is not active")
	wrapped := wrapInvalid(rejected)
	if !errors.Is(wrapped, mcpauth.ErrInvalidToken) {
		t.Error("a genuinely rejected token must read as an invalid token")
	}
}
