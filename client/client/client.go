package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	ftp "github.com/elwin/transmit2/client"
	"github.com/elwin/transmit2/mode"
)

func main() {

	if err := run(); err != nil {
		fmt.Print(err)
	}

}

func run() error {

	remote := "localhost:2121"

	conn, err := ftp.Dial(remote, ftp.DialWithDebugOutput(os.Stdout))
	if err != nil {
		return err
	}

	err = conn.Login("admin", "123456")
	if err != nil {
		return err
	}

	err = ReadAndWrite(conn)
	if err != nil {
		return err
	}

	err = conn.Mode(mode.ExtendedBlockMode)
	if err != nil {
		return err
	}

	err = conn.SetRetrOpts(10, 500)
	if err != nil {
		return err
	}

	err = ReadAndWrite(conn)
	if err != nil {
		return err
	}

	return conn.Quit()
}

func ReadAndWrite(conn *ftp.ServerConn) error {
	err := conn.Stor("stor.txt", strings.NewReader("Hello World!"))
	if err != nil {
		return err
	}

	res, err := conn.Retr("retr.txt")
	if err != nil {
		return err
	}

	f, err := os.Create("/Users/elwin/ftp/result.txt")
	if err != nil {
		return err
	}

	n, err := io.Copy(f, res)
	if err != nil {
		return err
	}

	res.Close()
	f.Close()

	fmt.Printf("Read %d bytes\n", n)

	entries, err := conn.List("/")
	if err != nil {
		return err
	}

	for _, entry := range entries {
		fmt.Printf("- %s (%d)\n", entry.Name, entry.Size)
	}

	return nil
}
