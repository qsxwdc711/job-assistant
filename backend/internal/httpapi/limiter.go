package httpapi

import (
	"sync"
	"time"
)

type loginBucket struct {
	expires time.Time
	count   int
}

type loginLimiter struct {
	mu      sync.Mutex
	buckets map[string]loginBucket
}

func (l *loginLimiter) allow(address string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, bucket := range l.buckets {
		if !bucket.expires.After(now) {
			delete(l.buckets, key)
		}
	}
	bucket, exists := l.buckets[address]
	if !exists {
		if len(l.buckets) >= 1024 {
			return false
		}
		bucket.expires = now.Add(time.Minute)
	}
	if bucket.count >= 10 {
		return false
	}
	bucket.count++
	l.buckets[address] = bucket
	return true
}
