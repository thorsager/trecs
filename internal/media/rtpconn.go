package media

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/thorsager/trecs/proto"
)

var rtpBufPool = sync.Pool{
	New: func() any {
		return make([]byte, 4096)
	},
}

var rtpWriteBufPool = sync.Pool{
	New: func() any {
		return make([]byte, 1500)
	},
}

var rtpPktPool = sync.Pool{
	New: func() any {
		return new(proto.RTPPacket)
	},
}

// RTPConn wraps a UDP socket for reading and writing RTP packets.
type RTPConn struct {
	conn *net.UDPConn
}

// NewRTPConn binds a UDP socket for RTP media using an OS-assigned port.
func NewRTPConn() (*RTPConn, error) {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	return &RTPConn{conn: conn}, nil
}

// NewRTPConnRange binds a UDP socket for RTP media within the given port range.
// If the range is invalid (min > max) or both are 0, an OS-assigned port is used.
func NewRTPConnRange(min, max int) (*RTPConn, error) {
	if min > 0 && max > 0 && max >= min {
		for port := min; port <= max; port++ {
			addr := &net.UDPAddr{IP: net.IPv4zero, Port: port}
			conn, err := net.ListenUDP("udp", addr)
			if err == nil {
				return &RTPConn{conn: conn}, nil
			}
		}
		return nil, fmt.Errorf("no available RTP port in range %d-%d", min, max)
	}
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	return &RTPConn{conn: conn}, nil
}

// LocalAddr returns the bound local address.
func (c *RTPConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// ReadRTP reads one RTP packet from the socket.
func (c *RTPConn) ReadRTP() (*proto.RTPPacket, net.Addr, error) {
	buf := rtpBufPool.Get().([]byte)
	n, addr, err := c.conn.ReadFromUDP(buf)
	if err != nil {
		rtpBufPool.Put(buf)
		return nil, nil, err
	}
	pkt := rtpPktPool.Get().(*proto.RTPPacket)
	pkt.Reset()
	if err := proto.UnmarshalRTPTo(buf[:n], pkt); err != nil {
		rtpBufPool.Put(buf)
		rtpPktPool.Put(pkt)
		return nil, nil, err
	}
	rtpBufPool.Put(buf)
	return pkt, addr, nil
}

// WriteRTP marshals and sends one RTP packet to addr.
// Uses a pooled buffer to avoid allocation on the marshal path.
func (c *RTPConn) WriteRTP(pkt *proto.RTPPacket, addr net.Addr) error {
	buf := rtpWriteBufPool.Get().([]byte)
	sz := pkt.MarshalSize()
	if sz > len(buf) {
		buf = make([]byte, sz)
	}
	n, err := pkt.MarshalTo(buf)
	if err != nil {
		if n == 0 || len(buf) > 1500 {
			rtpWriteBufPool.Put(buf)
		}
		return err
	}
	_, err = c.conn.WriteTo(buf[:n], addr)
	if len(buf) <= 1500 {
		rtpWriteBufPool.Put(buf)
	}
	return err
}

// SetReadDeadline sets the read deadline on the underlying socket.
func (c *RTPConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// Close closes the underlying UDP socket.
func (c *RTPConn) Close() error {
	return c.conn.Close()
}

// Release returns an RTPPacket obtained from ReadRTP back to the pool.
func (c *RTPConn) Release(pkt *proto.RTPPacket) {
	if pkt != nil {
		rtpPktPool.Put(pkt)
	}
}
