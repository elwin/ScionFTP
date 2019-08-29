package scion

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/scionproto/scion/go/lib/sciond"
	"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/lib/snet/squic"
	"github.com/scionproto/scion/go/lib/spath"
)

func Dial(local, remote Address, selector PathSelector) (*Connection, error) {

	err := initNetwork(local)
	if err != nil {
		return nil, err
	}

	err = setupPath(local.Addr(), remote.Addr(), selector)
	if err != nil {
		return nil, err
	}

	l := local.Addr()
	r := remote.Addr()

	session, err := squic.DialSCION(nil, &l, &r, nil)
	if err != nil {
		return nil, fmt.Errorf("unable to dial %s: %s", remote, err)
	}

	stream, err := session.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("unable to open stream: %s", err)
	}

	fmt.Println(session.LocalAddr())
	_, port, err := ParseCompleteAddress(session.LocalAddr().String())
	if err != nil {
		return nil, err
	}

	local.port = port

	err = sendHandshake(stream)
	if err != nil {
		return nil, err
	}

	return NewSQuicConnection(stream, local, remote), nil
}

func DialAddr(localAddr, remoteAddr string, selector PathSelector) (*Connection, error) {

	local, err := ConvertAddress(localAddr)
	if err != nil {
		return nil, err
	}

	remote, err := ConvertAddress(remoteAddr)
	if err != nil {
		return nil, err
	}

	return Dial(local, remote, selector)
}

func sendHandshake(rw io.ReadWriter) error {

	msg := []byte{200}

	binary.Write(rw, binary.BigEndian, msg)

	// log.Debug("Sent handshake", "msg", msg)

	return nil
}

func setupPath(local, remote snet.Addr, selector PathSelector) error {
	if !remote.IA.Eq(local.IA) {
		pathEntry := choosePath(local, remote, selector)
		if pathEntry == nil {
			return fmt.Errorf("no paths available to remote destination")
		}
		remote.Path = spath.New(pathEntry.Path.FwdPath)
		remote.Path.InitOffsets()
		remote.NextHop, _ = pathEntry.HostInfo.Overlay()
	}

	return nil
}

func choosePath(local, remote snet.Addr, selector PathSelector) *sciond.PathReplyEntry {
	var paths []*sciond.PathReplyEntry

	pathMgr := snet.DefNetwork.PathResolver()
	pathSet := pathMgr.Query(context.Background(), local.IA, remote.IA)

	if len(pathSet) == 0 {
		return nil
	}
	for _, p := range pathSet {
		paths = append(paths, p.Entry)
	}

	return selector(paths)
}

type PathSelector func([]*sciond.PathReplyEntry) *sciond.PathReplyEntry

func DefaultPathSelector(paths []*sciond.PathReplyEntry) *sciond.PathReplyEntry {
	return paths[0]
}

// Copied from Pingpong sample application:
// https://github.com/scionproto/scion/blob/8291539e5b23a217cb367bce6da05b71d0fe1d82/go/examples/pingpong/pingpong.go#L419
func InteractivePathSelector(paths []*sciond.PathReplyEntry) *sciond.PathReplyEntry {
	if len(paths) == 1 {
		return paths[0]
	}

	var index uint64

	fmt.Printf("Available paths to\n")
	for i := range paths {
		fmt.Printf("[%2d] %s\n", i, paths[i].Path.String())
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("Choose path: ")
		pathIndexStr, _ := reader.ReadString('\n')
		var err error
		index, err = strconv.ParseUint(pathIndexStr[:len(pathIndexStr)-1], 10, 64)
		if err == nil && int(index) < len(paths) {
			break
		}
		fmt.Fprintf(os.Stderr, "ERROR: Invalid path index, valid indices range: [0, %v]\n",
			len(paths))
	}

	return paths[index]
}

type Rotator struct {
	index int
	paths int
}

func NewRotator() *Rotator {
	return &Rotator{}
}

func (r *Rotator) Reset() {
	r.index = 0
}

func (r *Rotator) GetNumberOfUsedPaths() int {
	if r.index < r.paths {
		return r.index
	}

	return r.paths
}

func (r *Rotator) RotatingPathSelector(paths []*sciond.PathReplyEntry) *sciond.PathReplyEntry {
	r.paths = len(paths)
	index := r.index % r.paths
	fmt.Println(index)
	return paths[index]
}