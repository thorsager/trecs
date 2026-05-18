package sip

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

type FlowKey struct {
	SourceIP   string
	SourcePort int
	DestIP     string
	DestPort   int
	Transport  string
}

func FlowKeyFromConn(conn net.Conn) FlowKey {
	key := FlowKey{Transport: "TCP"}
	if remote, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		key.SourceIP = remote.IP.String()
		key.SourcePort = remote.Port
	}
	if local, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		key.DestIP = local.IP.String()
		key.DestPort = local.Port
	}
	return key
}

func (k FlowKey) String() string {
	return fmt.Sprintf("%s:%s:%d→%s:%d", k.Transport, k.SourceIP, k.SourcePort, k.DestIP, k.DestPort)
}

type FlowConn struct {
	Conn     net.Conn
	Key      FlowKey
	LastUsed time.Time
	cancel   context.CancelFunc
}

type FlowPool struct {
	mu     sync.Mutex
	flows  map[string]*FlowConn
	onDead func(flowID string)
}

func NewFlowPool(onDead func(flowID string)) *FlowPool {
	return &FlowPool{
		flows:  make(map[string]*FlowConn),
		onDead: onDead,
	}
}

func (p *FlowPool) Register(conn net.Conn) *FlowConn {
	key := FlowKeyFromConn(conn)
	fc := &FlowConn{
		Conn:     conn,
		Key:      key,
		LastUsed: time.Now(),
	}
	p.mu.Lock()
	p.flows[key.String()] = fc
	p.mu.Unlock()
	return fc
}

func (p *FlowPool) Unregister(key FlowKey) {
	p.mu.Lock()
	delete(p.flows, key.String())
	p.mu.Unlock()
}

func (p *FlowPool) Get(key FlowKey) *FlowConn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.flows[key.String()]
}

func (p *FlowPool) GetByFlowID(flowID string) *FlowConn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.flows[flowID]
}

func (p *FlowPool) GetByAddr(host string, port int) *FlowConn {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, fc := range p.flows {
		remote := fc.Conn.RemoteAddr().(*net.TCPAddr)
		if remote.IP.String() == host && remote.Port == port {
			return fc
		}
	}
	return nil
}

func (p *FlowPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.flows)
}

func (p *FlowPool) SetOnDead(fn func(string)) {
	p.mu.Lock()
	p.onDead = fn
	p.mu.Unlock()
}

func (p *FlowPool) RemoveDead(flowID string) {
	p.mu.Lock()
	fc := p.flows[flowID]
	delete(p.flows, flowID)
	p.mu.Unlock()
	if fc != nil && p.onDead != nil {
		p.onDead(flowID)
	}
}
