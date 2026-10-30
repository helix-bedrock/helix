package network

import (
	"net"
	"time"

	"github.com/helix-bedrock/protocol/conn"
	"github.com/helix-bedrock/protocol/login"
	"github.com/helix-bedrock/protocol/packet"
)

// Conn is a player's connection after the login sequence. Reads must come
// from one goroutine; writes, Flush and Close are safe from any.
type Conn struct {
	c     *conn.Conn
	login *Login
}

// Identity is who the player is, from the login token.
func (c *Conn) Identity() login.IdentityData { return c.login.Identity }

// Client is what the client said about itself at login.
func (c *Conn) Client() login.ClientData { return c.login.Client }

// Authenticated reports whether Xbox Live vouched for the player.
func (c *Conn) Authenticated() bool { return c.login.Authenticated }

// Protocol is the client's protocol version.
func (c *Conn) Protocol() int32 { return c.login.Protocol }

// CacheEnabled reports whether the client caches chunk blobs.
func (c *Conn) CacheEnabled() bool { return c.login.CacheEnabled }

// ReadPacket returns the next packet from the client.
func (c *Conn) ReadPacket() (packet.Packet, error) { return c.c.ReadPacket() }

// WritePacket queues a packet; it is sent at the next flush.
func (c *Conn) WritePacket(pk packet.Packet) error { return c.c.WritePacket(pk) }

// Flush sends the queued packets.
func (c *Conn) Flush() error { return c.c.Flush() }

// Disconnect sends the player a disconnect reason and message, and closes
// the connection.
func (c *Conn) Disconnect(reason int32, message string) {
	_ = c.c.WritePacket(&packet.Disconnect{Reason: reason, Message: message})
	_ = c.c.Flush()
	_ = c.c.Close()
}

// Close closes the connection.
func (c *Conn) Close() error { return c.c.Close() }

// RemoteAddr is the client's address.
func (c *Conn) RemoteAddr() net.Addr { return c.c.RemoteAddr() }

// Latency is the transport's measured round trip, or 0 if it measures none.
func (c *Conn) Latency() time.Duration {
	if l, ok := c.c.Transport().(interface{ Latency() time.Duration }); ok {
		return l.Latency()
	}
	return 0
}

// Hooks are the connection's own packet hooks.
func (c *Conn) Hooks() *conn.Hooks { return c.c.Hooks() }
