package auth

import (
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	lockoutWindow        = 15 * time.Minute
	userFailureThreshold = 5
	addrFailureThreshold = 20
	initialLockout       = time.Minute
	maxLockout           = 15 * time.Minute
	lockoutPruneInterval = time.Minute
)

type attemptRecord struct {
	failures     []time.Time
	lockedUntil  time.Time
	lockDuration time.Duration
}

type loginLimiter struct {
	mu        sync.Mutex
	now       func() time.Time
	users     map[string]*attemptRecord
	addrs     map[string]*attemptRecord
	lastPrune time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		now:   time.Now,
		users: make(map[string]*attemptRecord),
		addrs: make(map[string]*attemptRecord),
	}
}

func (l *loginLimiter) check(username, addr string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneLocked(now)
	wait := time.Duration(0)
	if rec := l.users[username]; rec != nil && rec.lockedUntil.After(now) {
		wait = rec.lockedUntil.Sub(now)
	}
	if rec := l.addrs[addr]; rec != nil && rec.lockedUntil.After(now) {
		if w := rec.lockedUntil.Sub(now); w > wait {
			wait = w
		}
	}
	return wait, wait > 0
}

func (l *loginLimiter) fail(username, addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneLocked(now)
	recordFailure(l.users, username, now, userFailureThreshold)
	recordFailure(l.addrs, addr, now, addrFailureThreshold)
}

func (l *loginLimiter) succeed(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.users, username)
}

func recordFailure(records map[string]*attemptRecord, key string, now time.Time, threshold int) {
	if key == "" {
		return
	}
	rec := records[key]
	if rec == nil {
		rec = &attemptRecord{}
		records[key] = rec
	}
	rec.failures = append(recentFailures(rec.failures, now), now)
	if rec.lockDuration == 0 && len(rec.failures) < threshold {
		return
	}
	switch {
	case rec.lockDuration == 0:
		rec.lockDuration = initialLockout
	case rec.lockDuration*2 > maxLockout:
		rec.lockDuration = maxLockout
	default:
		rec.lockDuration *= 2
	}
	rec.lockedUntil = now.Add(rec.lockDuration)
}

func recentFailures(failures []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-lockoutWindow)
	kept := failures[:0]
	for _, f := range failures {
		if !f.Before(cutoff) {
			kept = append(kept, f)
		}
	}
	return kept
}

func (l *loginLimiter) pruneLocked(now time.Time) {
	if now.Sub(l.lastPrune) < lockoutPruneInterval {
		return
	}
	l.lastPrune = now
	for _, records := range []map[string]*attemptRecord{l.users, l.addrs} {
		for key, rec := range records {
			rec.failures = recentFailures(rec.failures, now)
			if len(rec.failures) == 0 && !rec.lockedUntil.After(now) {
				delete(records, key)
			}
		}
	}
}

func retrySeconds(wait time.Duration) int {
	return int(math.Ceil(wait.Seconds()))
}

func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
