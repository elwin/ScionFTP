// Package ftp implements a FTP scionftp as described in RFC 959.
//
// A textproto.Error is returned for errors at the protocol level.
package ftp

import (
	"context"
	"io"
	"net"
	"net/textproto"
	"time"

	"github.com/elwin/scionFTP/scion"

	"github.com/elwin/scionFTP/logger"
)

// EntryType describes the different types of an Entry.
type EntryType int

// The differents types of an Entry
const (
	EntryTypeFile EntryType = iota
	EntryTypeFolder
	EntryTypeLink
)

// ServerConn represents the connection to a remote FTP server.
// A single connection only supports one in-flight data connection.
// It is not safe to be called concurrently.
type ServerConn struct {
	options       *dialOptions
	conn          *textproto.Conn
	local, remote string            // local and remote address (without port!)
	features      map[string]string // Server capabilities discovered at runtime
	mlstSupported bool
	extended      bool
	blockSize     int
	logger        logger.Logger
	selector      scion.PathSelector
}

// DialOption represents an option to start a new connection with Dial
type DialOption struct {
	setup func(do *dialOptions)
}

// dialOptions contains all the options set by DialOption.setup
type dialOptions struct {
	context     context.Context
	dialer      net.Dialer
	disableEPSV bool
	location    *time.Location
	debugOutput io.Writer
	blockSize   int
	selector    scion.PathSelector
}

// Entry describes a file and is returned by List().
type Entry struct {
	Name string
	Type EntryType
	Size uint64
	Time time.Time
}

// Dial connects to the specified address with optional options
func Dial(local, remote string, options ...DialOption) (*ServerConn, error) {
	do := &dialOptions{}
	for _, option := range options {
		option.setup(do)
	}

	if do.location == nil {
		do.location = time.UTC
	}

	ctx := do.context

	if ctx == nil {
		ctx = context.Background()
	}

	maxChunkSize := do.blockSize
	if maxChunkSize == 0 {
		maxChunkSize = 500
	}

	selector := do.selector
	if selector == nil {
		selector = scion.DefaultPathSelector
	}

	conn, err := scion.DialAddr(local, remote, selector)
	if err != nil {
		return nil, err
	}

	var sourceConn io.ReadWriteCloser = conn
	if do.debugOutput != nil {
		sourceConn = newDebugWrapper(conn, do.debugOutput)
	}

	localHost, _, err := scion.ParseAddress(local)
	if err != nil {
		return nil, err
	}

	remoteHost, _, err := scion.ParseAddress(remote)
	if err != nil {
		return nil, err
	}

	c := &ServerConn{
		options:   do,
		features:  make(map[string]string),
		conn:      textproto.NewConn(sourceConn),
		local:     localHost,
		remote:    remoteHost,
		logger:    &logger.StdLogger{},
		blockSize: maxChunkSize,
		selector:  selector,
	}

	_, _, err = c.conn.ReadResponse(StatusReady)
	if err != nil {
		c.Quit()
		return nil, err
	}

	err = c.feat()
	if err != nil {
		c.Quit()
		return nil, err
	}

	if _, mlstSupported := c.features["MLST"]; mlstSupported {
		c.mlstSupported = true
	}

	return c, nil
}

// DialWithTimeout returns a DialOption that configures the ServerConn with specified timeout
func DialWithTimeout(timeout time.Duration) DialOption {
	return DialOption{func(do *dialOptions) {
		do.dialer.Timeout = timeout
	}}
}

// DialWithDialer returns a DialOption that configures the ServerConn with specified net.Dialer
func DialWithDialer(dialer net.Dialer) DialOption {
	return DialOption{func(do *dialOptions) {
		do.dialer = dialer
	}}
}

// DialWithDisabledEPSV returns a DialOption that configures the ServerConn with EPSV disabled
// Note that EPSV is only used when advertised in the server features.
func DialWithDisabledEPSV(disabled bool) DialOption {
	return DialOption{func(do *dialOptions) {
		do.disableEPSV = disabled
	}}
}

// DialWithLocation returns a DialOption that configures the ServerConn with specified time.Location
// The location is used to parse the dates sent by the server which are in server's timezone
func DialWithLocation(location *time.Location) DialOption {
	return DialOption{func(do *dialOptions) {
		do.location = location
	}}
}

// DialWithContext returns a DialOption that configures the ServerConn with specified context
// The context will be used for the initial connection setup
func DialWithContext(ctx context.Context) DialOption {
	return DialOption{func(do *dialOptions) {
		do.context = ctx
	}}
}

// DialWithDebugOutput returns a DialOption that configures the ServerConn to write to the Writer
// everything it reads from the server
func DialWithDebugOutput(w io.Writer) DialOption {
	return DialOption{func(do *dialOptions) {
		do.debugOutput = w
	}}
}

// DialWithBlockSize sets the maximum blocksize to be used at the start but only clientside,
// alternatively we can set it with the command OPTS RETR (SetRetrOpts)
func DialWithBlockSize(blockSize int) DialOption {
	return DialOption{func(do *dialOptions) {
		do.blockSize = blockSize
	}}
}

// DialWithPathSelector sets the selector to be used. The default (DefaultPathSelector) just picks
// the first path
func DialWithPathSelector(selector scion.PathSelector) DialOption {
	return DialOption{func(do *dialOptions) {
		do.selector = selector
	}}
}

// DialTimeout initializes the connection to the specified ftp server address.
//
// It is generally followed by a call to Login() as most FTP commands require
// an authenticated user.
func DialTimeout(local, remote string, timeout time.Duration) (*ServerConn, error) {
	return Dial(local, remote, DialWithTimeout(timeout))
}
