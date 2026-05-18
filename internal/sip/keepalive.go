package sip

import (
	"context"
	"log"
	"net"
	"sync"
	"time"
)

const DefaultKeepaliveInterval = 10 * time.Second

type KeepaliveTracker struct {
	mu         sync.Mutex
	lastActive time.Time
	interval   time.Duration
}

func NewKeepaliveTracker(interval time.Duration) *KeepaliveTracker {
	return &KeepaliveTracker{
		lastActive: time.Now(),
		interval:   interval,
	}
}

func (kt *KeepaliveTracker) UpdateActivity() {
	kt.mu.Lock()
	kt.lastActive = time.Now()
	kt.mu.Unlock()
}

func (kt *KeepaliveTracker) Run(ctx context.Context, conn net.Conn, flowID string) {
	ticker := time.NewTicker(kt.interval / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var idle bool
			kt.mu.Lock()
			if time.Since(kt.lastActive) >= kt.interval {
				idle = true
			}
			kt.mu.Unlock()

			if idle {
				_, err := conn.Write([]byte("\r\n\r\n"))
				if err != nil {
					log.Printf("Keepalive write error on flow %s: %v", flowID, err)
					return
				}
			}
		}
	}
}
