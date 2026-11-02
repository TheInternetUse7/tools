package limiter

import (
	"sync"
	"time"
)

// ConcurrencyLimiter manages execution slots using a buffered channel.
type ConcurrencyLimiter struct {
	sem chan struct{}
	max int
}

// NewConcurrencyLimiter creates a limiter with the given maximum concurrent slots.
func NewConcurrencyLimiter(max int) *ConcurrencyLimiter {
	if max < 1 {
		max = 1
	}
	return &ConcurrencyLimiter{
		sem: make(chan struct{}, max),
		max: max,
	}
}

// TryAcquire attempts to acquire an execution slot without blocking.
// Returns true if acquired, or false if all slots are currently busy.
func (c *ConcurrencyLimiter) TryAcquire() bool {
	select {
	case c.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release frees an acquired execution slot.
func (c *ConcurrencyLimiter) Release() {
	select {
	case <-c.sem:
	default:
	}
}

// Active returns the number of currently running jobs.
func (c *ConcurrencyLimiter) Active() int {
	return len(c.sem)
}

// Max returns the maximum concurrency capacity.
func (c *ConcurrencyLimiter) Max() int {
	return c.max
}

// ipTracker records timestamps of requests from a specific IP.
type ipTracker struct {
	timestamps []time.Time
}

// IPRateLimiter provides sliding-window rate limiting per IP address.
type IPRateLimiter struct {
	mu          sync.Mutex
	limits      map[string]*ipTracker
	maxRequests int
	window      time.Duration
	stopCh      chan struct{}
}

// NewIPRateLimiter creates a new rate limiter that allows maxRequests within the given window.
func NewIPRateLimiter(maxRequests int, window time.Duration) *IPRateLimiter {
	if maxRequests < 1 {
		maxRequests = 5
	}
	if window <= 0 {
		window = 10 * time.Minute
	}

	limiter := &IPRateLimiter{
		limits:      make(map[string]*ipTracker),
		maxRequests: maxRequests,
		window:      window,
		stopCh:      make(chan struct{}),
	}

	// Periodically sweep expired IPs to prevent unbounded memory growth on tiny VPS
	go limiter.cleanupLoop()

	return limiter
}

// Allow checks if the given IP address is allowed to proceed.
// Returns allowed status, remaining requests in the window, and time until reset.
func (l *IPRateLimiter) Allow(ip string) (bool, int, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	tracker, exists := l.limits[ip]
	if !exists {
		tracker = &ipTracker{timestamps: make([]time.Time, 0, l.maxRequests)}
		l.limits[ip] = tracker
	}

	// Filter out timestamps older than the window
	valid := tracker.timestamps[:0]
	for _, ts := range tracker.timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	tracker.timestamps = valid

	if len(tracker.timestamps) >= l.maxRequests {
		oldest := tracker.timestamps[0]
		resetIn := oldest.Add(l.window).Sub(now)
		if resetIn < 0 {
			resetIn = 0
		}
		return false, 0, resetIn
	}

	tracker.timestamps = append(tracker.timestamps, now)
	remaining := l.maxRequests - len(tracker.timestamps)
	return true, remaining, 0
}

// cleanupLoop removes stale entries every 5 minutes.
func (l *IPRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case now := <-ticker.C:
			l.mu.Lock()
			cutoff := now.Add(-l.window)
			for ip, tracker := range l.limits {
				// If the latest request is older than the window, delete the entry
				if len(tracker.timestamps) == 0 || tracker.timestamps[len(tracker.timestamps)-1].Before(cutoff) {
					delete(l.limits, ip)
				}
			}
			l.mu.Unlock()
		}
	}
}

// Stop halts the background cleanup goroutine.
func (l *IPRateLimiter) Stop() {
	close(l.stopCh)
}
