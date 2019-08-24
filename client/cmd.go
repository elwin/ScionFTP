package ftp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	mode2 "github.com/elwin/transmit2/mode"

	"github.com/elwin/transmit2/socket"
)

// Login authenticates the client with specified user and password.
//
// "anonymous"/"anonymous" is a common user/password scheme for FTP servers
// that allows anonymous read-only accounts.
func (c *ServerConn) Login(user, password string) error {
	code, message, err := c.cmd(-1, "USER %s", user)
	if err != nil {
		return err
	}

	switch code {
	case StatusLoggedIn:
	case StatusUserOK:
		_, _, err = c.cmd(StatusLoggedIn, "PASS %s", password)
		if err != nil {
			return err
		}
	default:
		return errors.New(message)
	}

	// Switch to binary mode
	if _, _, err = c.cmd(StatusCommandOK, "TYPE I"); err != nil {
		return err
	}

	// Switch to UTF-8
	err = c.setUTF8()

	return err
}

// feat issues a FEAT FTP command to list the additional commands supported by
// the remote FTP server.
// FEAT is described in RFC 2389
func (c *ServerConn) feat() error {
	code, message, err := c.cmd(-1, "FEAT")
	if err != nil {
		return err
	}

	if code != StatusSystem {
		// The server does not support the FEAT command. This is not an
		// error: we consider that there is no additional feature.
		return nil
	}

	lines := strings.Split(message, "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, " ") {
			continue
		}

		line = strings.TrimSpace(line)
		featureElements := strings.SplitN(line, " ", 2)

		command := featureElements[0]

		var commandDesc string
		if len(featureElements) == 2 {
			commandDesc = featureElements[1]
		}

		c.features[command] = commandDesc
	}

	return nil
}

// setUTF8 issues an "OPTS UTF8 ON" command.
func (c *ServerConn) setUTF8() error {
	if _, ok := c.features["UTF8"]; !ok {
		return nil
	}

	code, message, err := c.cmd(-1, "OPTS UTF8 ON")
	if err != nil {
		return err
	}

	// Workaround for FTP servers, that does not support this option.
	if code == StatusBadArguments {
		return nil
	}

	// The ftpd "filezilla-server" has FEAT support for UTF8, but always returns
	// "202 UTF8 mode is always enabled. No need to send this command." when
	// trying to use it. That's OK
	if code == StatusCommandNotImplemented {
		return nil
	}

	if code != StatusCommandOK {
		return errors.New(message)
	}

	return nil
}

func (c *ServerConn) SetRetrOpts(parallelism, blockSize int) error {
	if parallelism < 1 {
		return fmt.Errorf("parallelism needs to be at least 1")
	}

	if blockSize < 1 {
		return fmt.Errorf("block size needs to be at least 1")
	}

	parallelOpts := "Parallelism=" + strconv.Itoa(parallelism) + ";"
	layoutOpts := "StripeLayout=Blocked;BlockSize=" + strconv.Itoa(blockSize) + ";"

	code, message, err := c.cmd(-1, "OPTS RETR "+parallelOpts+layoutOpts)
	if err != nil {
		return err
	}

	if code != StatusCommandOK {
		return fmt.Errorf("failed to set options: %s", message)
	}

	c.BlockSize = blockSize

	return nil
}

// epsv issues an "EPSV" command to get a port number for a data connection.
func (c *ServerConn) epsv() (port int, err error) {
	_, line, err := c.cmd(StatusExtendedPassiveMode, "EPSV")
	if err != nil {
		return
	}

	start := strings.Index(line, "|||")
	end := strings.LastIndex(line, "|")
	if start == -1 || end == -1 {
		err = errors.New("invalid EPSV response format")
		return
	}
	port, err = strconv.Atoi(line[start+3 : end])
	return
}

// pasv issues a "PASV" command to get a port number for a data connection.
func (c *ServerConn) pasv() (port int, err error) {
	return c.epsv()
}

// getDataConnPort returns a host, port for a new data connection
// it uses the best available method to do so
func (c *ServerConn) getDataConnPort() (int, error) {
	return c.pasv()
}

// openDataConn creates a new FTP data connection.
func (c *ServerConn) openDataConn() (socket.DataSocket, error) {

	if c.extended {

		addrs, err := c.spas()
		if err != nil {
			return nil, err
		}

		sockets := make([]socket.DataSocket, len(addrs))
		for i := range sockets {
			conn, err := net.Dial("tcp", addrs[i])

			if err != nil {

				// Close already opened sockets
				for j := 0; j < i; j++ {
					sockets[j].Close()
				}

				return nil, err
			}

			sockets[i] = socket.NewScionSocket(conn)

		}

		return socket.NewMultiSocket(sockets, c.BlockSize), nil

	} else {

		port, err := c.getDataConnPort()
		if err != nil {
			return nil, err
		}

		conn, err := net.Dial("tcp", c.remote+":"+strconv.Itoa(port))
		if err != nil {
			return nil, err
		}

		return socket.NewScionSocket(conn), nil
	}
}

// cmd is a helper function to execute a command and check for the expected FTP
// return code
func (c *ServerConn) cmd(expected int, format string, args ...interface{}) (int, string, error) {
	_, err := c.conn.Cmd(format, args...)
	if err != nil {
		return 0, "", err
	}

	return c.conn.ReadResponse(expected)
}

// cmdDataConnFrom executes a command which require a FTP data connection.
// Issues a REST FTP command to specify the number of bytes to skip for the transfer.
func (c *ServerConn) cmdDataConnFrom(offset uint64, format string, args ...interface{}) (socket.DataSocket, error) {
	conn, err := c.openDataConn()
	if err != nil {
		return nil, err
	}

	if offset != 0 {
		_, _, err := c.cmd(StatusRequestFilePending, "REST %d", offset)
		if err != nil {
			conn.Close()
			return nil, err
		}
	}

	_, err = c.conn.Cmd(format, args...)
	if err != nil {
		conn.Close()
		return nil, err
	}

	code, msg, err := c.conn.ReadResponse(-1)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if code != StatusAlreadyOpen && code != StatusAboutToSend {
		conn.Close()
		return nil, &textproto.Error{Code: code, Msg: msg}
	}

	return conn, nil
}

// NameList issues an NLST FTP command.
func (c *ServerConn) NameList(path string) (entries []string, err error) {
	conn, err := c.cmdDataConnFrom(0, "NLST %s", path)
	if err != nil {
		return
	}

	r := &Response{conn: conn, c: c}
	defer r.Close()

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		entries = append(entries, scanner.Text())
	}
	if err = scanner.Err(); err != nil {
		return entries, err
	}
	return
}

// List issues a LIST FTP command.
func (c *ServerConn) List(path string) (entries []*Entry, err error) {

	var cmd string
	var parser parseFunc

	if c.mlstSupported {
		cmd = "MLSD"
		parser = parseRFC3659ListLine
	} else {
		cmd = "LIST"
		parser = parseListLine
	}

	conn, err := c.cmdDataConnFrom(0, "%s %s", cmd, path)
	if err != nil {
		return
	}

	r := &Response{conn: conn, c: c}
	defer r.Close()

	scanner := bufio.NewScanner(r)
	now := time.Now()
	for scanner.Scan() {
		entry, err := parser(scanner.Text(), now, c.options.location)
		if err == nil {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return
}

// ChangeDir issues a CWD FTP command, which changes the current directory to
// the specified path.
func (c *ServerConn) ChangeDir(path string) error {
	_, _, err := c.cmd(StatusRequestedFileActionOK, "CWD %s", path)
	return err
}

// ChangeDirToParent issues a CDUP FTP command, which changes the current
// directory to the parent directory.  This is similar to a call to ChangeDir
// with a path set to "..".
func (c *ServerConn) ChangeDirToParent() error {
	_, _, err := c.cmd(StatusRequestedFileActionOK, "CDUP")
	return err
}

// CurrentDir issues a PWD FTP command, which Returns the path of the current
// directory.
func (c *ServerConn) CurrentDir() (string, error) {
	_, msg, err := c.cmd(StatusPathCreated, "PWD")
	if err != nil {
		return "", err
	}

	start := strings.Index(msg, "\"")
	end := strings.LastIndex(msg, "\"")

	if start == -1 || end == -1 {
		return "", errors.New("unsuported PWD response format")
	}

	return msg[start+1 : end], nil
}

// FileSize issues a SIZE FTP command, which Returns the size of the file
func (c *ServerConn) FileSize(path string) (int64, error) {
	_, msg, err := c.cmd(StatusFile, "SIZE %s", path)
	if err != nil {
		return 0, err
	}

	return strconv.ParseInt(msg, 10, 64)
}

// Retr issues a RETR FTP command to fetch the specified file from the remote
// FTP server.
//
// The returned ReadCloser must be closed to cleanup the FTP data connection.
func (c *ServerConn) Retr(path string) (*Response, error) {
	return c.RetrFrom(path, 0)
}

// RetrFrom issues a RETR FTP command to fetch the specified file from the remote
// FTP server, the server will not send the offset first bytes of the file.
//
// The returned ReadCloser must be closed to cleanup the FTP data connection.
func (c *ServerConn) RetrFrom(path string, offset uint64) (*Response, error) {
	conn, err := c.cmdDataConnFrom(offset, "RETR %s", path)
	if err != nil {
		return nil, err
	}

	return &Response{conn: conn, c: c}, nil
}

// Stor issues a STOR FTP command to store a file to the remote FTP server.
// Stor creates the specified file with the content of the io.Reader.
//
// Hint: io.Pipe() can be used if an io.Writer is required.
func (c *ServerConn) Stor(path string, r io.Reader) error {
	return c.StorFrom(path, r, 0)
}

// StorFrom issues a STOR FTP command to store a file to the remote FTP server.
// Stor creates the specified file with the content of the io.Reader, writing
// on the server will start at the given file offset.
//
// Hint: io.Pipe() can be used if an io.Writer is required.
func (c *ServerConn) StorFrom(path string, r io.Reader, offset uint64) error {
	conn, err := c.cmdDataConnFrom(offset, "STOR %s", path)
	if err != nil {
		return err
	}

	_, err = io.Copy(conn, r)
	conn.Close()
	if err != nil {
		return err
	}

	_, _, err = c.conn.ReadResponse(StatusClosingDataConnection)
	return err
}

// Rename renames a file on the remote FTP server.
func (c *ServerConn) Rename(from, to string) error {
	_, _, err := c.cmd(StatusRequestFilePending, "RNFR %s", from)
	if err != nil {
		return err
	}

	_, _, err = c.cmd(StatusRequestedFileActionOK, "RNTO %s", to)
	return err
}

// Delete issues a DELE FTP command to delete the specified file from the
// remote FTP server.
func (c *ServerConn) Delete(path string) error {
	_, _, err := c.cmd(StatusRequestedFileActionOK, "DELE %s", path)
	return err
}

// RemoveDirRecur deletes a non-empty folder recursively using
// RemoveDir and Delete
func (c *ServerConn) RemoveDirRecur(path string) error {
	err := c.ChangeDir(path)
	if err != nil {
		return err
	}
	currentDir, err := c.CurrentDir()
	if err != nil {
		return err
	}

	entries, err := c.List(currentDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.Name != ".." && entry.Name != "." {
			if entry.Type == EntryTypeFolder {
				err = c.RemoveDirRecur(currentDir + "/" + entry.Name)
				if err != nil {
					return err
				}
			} else {
				err = c.Delete(entry.Name)
				if err != nil {
					return err
				}
			}
		}
	}
	err = c.ChangeDirToParent()
	if err != nil {
		return err
	}
	err = c.RemoveDir(currentDir)
	return err
}

// MakeDir issues a MKD FTP command to create the specified directory on the
// remote FTP server.
func (c *ServerConn) MakeDir(path string) error {
	_, _, err := c.cmd(StatusPathCreated, "MKD %s", path)
	return err
}

// RemoveDir issues a RMD FTP command to remove the specified directory from
// the remote FTP server.
func (c *ServerConn) RemoveDir(path string) error {
	_, _, err := c.cmd(StatusRequestedFileActionOK, "RMD %s", path)
	return err
}

// NoOp issues a NOOP FTP command.
// NOOP has no effects and is usually used to prevent the remote FTP server to
// close the otherwise idle connection.
func (c *ServerConn) NoOp() error {
	_, _, err := c.cmd(StatusCommandOK, "NOOP")
	return err
}

// Logout issues a REIN FTP command to logout the current user.
func (c *ServerConn) Logout() error {
	_, _, err := c.cmd(StatusReady, "REIN")
	return err
}

// Quit issues a QUIT FTP command to properly close the connection from the
// remote FTP server.
func (c *ServerConn) Quit() error {
	c.conn.Cmd("QUIT")
	return c.conn.Close()
}

// GridFTP Extensions (https://www.ogf.org/documents/GFD.20.pdf)

// Switch Mode
func (c *ServerConn) Mode(mode byte) error {
	code, line, err := c.cmd(StatusCommandOK, "MODE %s", string(mode))

	if err != nil {
		return fmt.Errorf("failed to set Mode %v: %d - %s", mode, code, line)
	}

	if mode == mode2.Stream {
		c.extended = false
	} else if mode == mode2.ExtendedBlockMode {
		c.extended = true
	}

	return nil
}

// Striped Passive
//
// This command is analogous to the PASV command, but allows an array of
// host/port connections to be returned. This enables STRIPING, that is,
// multiple network endpoints (multi-homed hosts, or multiple hosts) to
// participate in the transfer.
func (c *ServerConn) spas() ([]string, error) {
	_, line, err := c.cmd(StatusExtendedPassiveMode, "SPAS")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(line, "\n")

	var addrs []string

	for _, line = range lines {
		if !strings.HasPrefix(line, " ") {
			continue
		}

		addr := strings.TrimLeft(line, " ")

		addrs = append(addrs, addr)
	}

	return addrs, nil
}

// Extended Retrieve
//
// This is analogous to the RETR command, but it allows the data to be
// manipulated (typically reduced in size) before being transmitted.
func (c *ServerConn) Eret(path string, offset, length int) (*Response, error) {

	conn, err := c.cmdDataConnFrom(0, "ERET PFT=\"%d,%d\" %s", offset, length, path)

	if err != nil {
		return nil, err
	}

	return &Response{conn: conn, c: c}, nil
}
