package socket

import (
	"net"
	"time"
)

// DataSocket describes a data socket is used to send non-control data between the client and
// server.
type DataSocket interface {
	// the standard io.Reader interface
	Read(p []byte) (n int, err error)

	// the standard io.ReaderFrom interface
	// ReadFrom(r io.Reader) (int64, error)

	// the standard io.Writer interface
	Write(p []byte) (n int, err error)

	// the standard io.Closer interface
	Close() error

	// Set deadline associated with connection (client)
	SetDeadline(t time.Time) error
}

var _ DataSocket = &ScionSocket{}

type ScionSocket struct {
	net.Conn
}

func NewScionSocket(conn net.Conn) *ScionSocket {
	return &ScionSocket{conn}
}
