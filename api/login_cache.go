package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"sync"
	"time"

	gerrors "github.com/shaj13/go-guardian/errors"
)

// Logging in without paying for bcrypt twice.
//
// The app refreshes its token every ten minutes by logging in again, and bcrypt
// is deliberately slow, so remembering a recent success is worth having. What
// the cache must never do is answer for the database.
//
// The cache this replaces did exactly that. It kept the password itself, keyed
// by whatever casing the client sent, for a hundred years, and a hit never
// looked at the user again. After a password change the new password was
// refused and the old one kept working until the dyno restarted, and a
// suspension or deactivation never reached anyone already cached. Clearing it
// only helped on the one dyno that handled the change.
//
// Now every login reads the user first. The cache is keyed by user ID and holds
// the stored bcrypt hash next to a keyed digest of the password that matched
// it. It answers only while both still agree, so writing a new password hash
// retires every dyno's entry at once, and the deactivation and suspension
// checks always run.

const (
	// loginCacheTTL bounds how long a digest lives in memory. Correctness does
	// not depend on it; it only keeps idle entries from accumulating.
	loginCacheTTL = time.Hour
	// loginCacheMaxEntries caps memory. Past it, expired entries are dropped,
	// and if that is not enough the cache starts again. The cost of emptying it
	// is one bcrypt per login.
	loginCacheMaxEntries = 50000
)

type loginCacheEntry struct {
	storedHash string
	digest     []byte
	expires    time.Time
}

type loginCache struct {
	mu      sync.Mutex
	key     []byte
	ttl     time.Duration
	max     int
	now     func() time.Time
	entries map[string]loginCacheEntry
}

// newLoginCache returns a cache with a fresh random key. The key never leaves
// the process, so a digest is useless to anyone who reads it out of memory. If
// no key can be made there is no cache, and every login pays for bcrypt.
func newLoginCache(ttl time.Duration, max int) *loginCache {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil
	}
	return &loginCache{
		key:     key,
		ttl:     ttl,
		max:     max,
		now:     time.Now,
		entries: make(map[string]loginCacheEntry),
	}
}

func (c *loginCache) digest(password string) []byte {
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte(password))
	return mac.Sum(nil)
}

// matches reports whether this password already matched this stored hash.
// A different stored hash means the password was changed since, so the entry
// is dead whatever the password.
func (c *loginCache) matches(userID, storedHash, password string) bool {
	if c == nil || userID == "" || storedHash == "" {
		return false
	}
	c.mu.Lock()
	entry, ok := c.entries[userID]
	c.mu.Unlock()
	if !ok || c.now().After(entry.expires) || entry.storedHash != storedHash {
		return false
	}
	return hmac.Equal(entry.digest, c.digest(password))
}

// remember records a password that bcrypt has just confirmed.
func (c *loginCache) remember(userID, storedHash, password string) {
	if c == nil || userID == "" || storedHash == "" {
		return
	}
	entry := loginCacheEntry{
		storedHash: storedHash,
		digest:     c.digest(password),
		expires:    c.now().Add(c.ttl),
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[userID]; !exists && len(c.entries) >= c.max {
		now := c.now()
		for id, e := range c.entries {
			if now.After(e.expires) {
				delete(c.entries, id)
			}
		}
		if len(c.entries) >= c.max {
			c.entries = make(map[string]loginCacheEntry)
		}
	}
	c.entries[userID] = entry
}

// errAuthUnavailable means the user could not be looked up at all, as opposed
// to being looked up and refused. It must not be answered with a 401: the app
// treats a 401 as a wrong password and deletes the saved login, so a slow
// database used to sign out everyone who refreshed during it.
var errAuthUnavailable = errors.New("sign-in is temporarily unavailable, please try again in a moment")

// isAuthUnavailable finds errAuthUnavailable inside the authenticator's
// combined error, which collects one error per strategy tried.
func isAuthUnavailable(err error) bool {
	if errors.Is(err, errAuthUnavailable) {
		return true
	}
	var multi gerrors.MultiError
	if errors.As(err, &multi) {
		for _, e := range multi {
			if errors.Is(e, errAuthUnavailable) {
				return true
			}
		}
	}
	return false
}
