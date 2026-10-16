package relay

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ipLimiter slows an address that keeps sending wrong pairing codes: the first few failures are
// free, then each one locks the address out, for a while that doubles up to a cap. A 40-bit code
// that lives five minutes is not guessable anyway; this keeps the log quiet and the CPU idle
// under a scan.
type ipLimiter struct {
	mu    sync.Mutex
	by    map[string]*ipState
	free  int
	first time.Duration
	max   time.Duration
}

type ipState struct {
	failures int
	lock     time.Duration // the last lock's length; 0 = none yet
	until    time.Time     // locked out until
	last     time.Time     // the last failure
}

func newIPLimiter(free int, first, max time.Duration) *ipLimiter {
	return &ipLimiter{by: map[string]*ipState{}, free: free, first: first, max: max}
}

// allow says whether the address may try now; when not, how long until it may.
func (l *ipLimiter) allow(ip string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.by[ip]
	if st == nil || !now.Before(st.until) {
		return true, 0
	}
	return false, st.until.Sub(now)
}

// failed counts a wrong code; past the free ones the address is locked out.
func (l *ipLimiter) failed(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.by) > 1000 { // quiet addresses go, so the map does not grow with the internet
		for k, st := range l.by {
			if now.Sub(st.last) > time.Hour && !now.Before(st.until) {
				delete(l.by, k)
			}
		}
	}
	st := l.by[ip]
	if st == nil {
		st = &ipState{}
		l.by[ip] = st
	}
	st.failures++
	st.last = now
	if st.failures > l.free {
		if st.lock == 0 {
			st.lock = l.first
		} else {
			st.lock = min(st.lock*2, l.max)
		}
		st.until = now.Add(st.lock)
	}
}

// succeeded forgets the address: a right code ends the count.
func (l *ipLimiter) succeeded(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.by, ip)
}

// clientIP is the address a request came from: what the relay's own reverse proxy says
// (X-Real-IP, else the last X-Forwarded-For entry, the one it appended), else the connection's.
func clientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		parts := strings.Split(v, ",")
		if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
			return last
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
