package media

import (
	"fmt"
	"net"
	"time"

	"gitub.com/thorsager/trec/proto"
)

var (
	RTPPortMin = 0
	RTPPortMax = 0
)

// RTPConn wraps a UDP socket for reading and writing RTP packets.
type RTPConn struct {
	conn *net.UDPConn
}

// NewRTPConn binds a UDP socket for RTP media. If RTPPortMin/RTPPortMax
// are set (non-zero), it tries ports within that range; otherwise it uses
// a random OS-assigned port.
func NewRTPConn() (*RTPConn, error) {
	if RTPPortMin > 0 && RTPPortMax > 0 && RTPPortMax >= RTPPortMin {
		for port := RTPPortMin; port <= RTPPortMax; port++ {
			addr := &net.UDPAddr{IP: net.IPv4zero, Port: port}
			conn, err := net.ListenUDP("udp", addr)
			if err == nil {
				return &RTPConn{conn: conn}, nil
			}
		}
		return nil, fmt.Errorf("no available RTP port in range %d-%d", RTPPortMin, RTPPortMax)
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
	buf := make([]byte, 4096)
	n, addr, err := c.conn.ReadFromUDP(buf)
	if err != nil {
		return nil, nil, err
	}
	pkt, err := proto.UnmarshalRTP(buf[:n])
	if err != nil {
		return nil, nil, err
	}
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
