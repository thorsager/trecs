package media

import (
	"context"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	"gitub.com/thorsager/trec/proto"
)

const bridgeReadTimeout = 1 * time.Second

type Bridge struct {
	AConn    *RTPConn
	BConn    *RTPConn
	aRemote  net.Addr
	bRemote  net.Addr
	ASToBSS uint32
	BSToASS uint32
	started  bool
	mu       sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
}

func NewBridge(aConn, bConn *RTPConn) *Bridge {
	ctx, cancel := context.WithCancel(context.Background())
	return &Bridge{
		AConn:   aConn,
		BConn:   bConn,
		ASToBSS: rand.Uint32(),
		BSToASS: rand.Uint32(),
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (b *Bridge) SetARemote(addr net.Addr) {
	b.mu.Lock()
	b.aRemote = addr
	b.mu.Unlock()
}

func (b *Bridge) SetBRemote(addr net.Addr) {
	b.mu.Lock()
	b.bRemote = addr
	b.mu.Unlock()
}

func (b *Bridge) Start() {
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return
	}
	if b.aRemote == nil || b.bRemote == nil {
		b.mu.Unlock()
		log.Printf("Bridge: cannot start — remote addresses not set")
		return
	}
	b.started = true
	b.mu.Unlock()

	go b.forward(b.AConn, b.BConn, b.bRemote, b.ASToBSS, "A→B")
	go b.forward(b.BConn, b.AConn, b.aRemote, b.BSToASS, "B→A")
	log.Printf("Bridge started: A↔B")
}

func (b *Bridge) Stop() {
	b.cancel()
}

func (b *Bridge) forward(src, dst *RTPConn, remote net.Addr, ssrc uint32, dir string) {
	var seq uint16
	var timestamp uint32
	out := &proto.RTPPacket{
		Header: proto.RTPHeader{Version: 2, SSRC: ssrc},
	}
	marshalBuf := make([]byte, 1500)

	for {
		if err := src.SetReadDeadline(time.Now().Add(bridgeReadTimeout)); err != nil {
			return
		}

		pkt, _, err := src.ReadRTP()
		if err != nil {
			select {
			case <-b.ctx.Done():
				return
			default:
				continue
			}
		}

		out.Header.Padding = pkt.Header.Padding
		out.Header.Marker = pkt.Header.Marker
		out.Header.PayloadType = pkt.Header.PayloadType
		out.Header.SequenceNumber = seq
		out.Header.Timestamp = timestamp
		out.Payload = pkt.Payload

		sz := out.MarshalSize()
		if sz > len(marshalBuf) {
			marshalBuf = make([]byte, sz)
		}
		n, err := out.MarshalTo(marshalBuf)
		if err != nil {
			rtpPktPool.Put(pkt)
			return
		}
		if _, err := dst.conn.WriteTo(marshalBuf[:n], remote); err != nil {
			rtpPktPool.Put(pkt)
			return
		}
		rtpPktPool.Put(pkt)
		seq++
		timestamp += samplesPerFrame
	}
}
