package media

import (
	"fmt"
	"net"
	"sync"
	"time"

	"gitub.com/thorsager/trec/proto"
)

var rtpBufPool = sync.Pool{
		New: func() any {
			return make([]byte, 4096)
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
	pkt, err := proto.UnmarshalRTP(buf[:n])
	if err != nil {
		rtpBufPool.Put(buf)
		return nil, nil, err
	}
	payload := make([]byte, len(pkt.Payload))
	copy(payload, pkt.Payload)
	pkt.Payload = payload
	rtpBufPool.Put(buf)
	return &pkt, addr, nil
}

// WriteRTP marshals and sends one RTP packet to addr.
func (c *RTPConn) WriteRTP(pkt *proto.RTPPacket, addr net.Addr) error {
	data, err := pkt.Marshal()
	if err != nil {
		return err
	}
	_, err = c.conn.WriteTo(data, addr)
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
