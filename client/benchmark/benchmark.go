package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"strconv"
	"time"

	"github.com/elwin/transmit2/mode"

	ftp "github.com/elwin/transmit2/client"
)

const (
	remote = "localhost:2121"
)

func main() {

	if err := run(); err != nil {
		fmt.Println(err)
	}

}

func run() error {

	conn, err := ftp.Dial(remote)
	if err != nil {
		return err
	}

	if err = conn.Login("admin", "123456"); err != nil {
		return err
	}

	err = conn.Mode(mode.ExtendedBlockMode)
	if err != nil {
		return err
	}

	tests := []struct {
		parallelism int
		duration    time.Duration
	}{
		{parallelism: 1},
		{parallelism: 2},
		{parallelism: 4},
		{parallelism: 8},
		{parallelism: 16},
		{parallelism: 32},
	}

	for j := 0; j < 10; j++ {
		for i := range tests {

			err = conn.SetRetrOpts(tests[i].parallelism, 500)
			if err != nil {
				return err
			}

			size := 1000000

			start := time.Now()
			resp, err := conn.Retr(strconv.Itoa(size))
			if err != nil {
				return err
			}

			n, err := io.Copy(ioutil.Discard, resp)
			if err != nil {
				return err
			}
			if int(n) != size {
				return fmt.Errorf("failed to read correct number of bytes, expected %d but got %d", size, n)
			}
			resp.Close()

			tests[i].duration += time.Since(start)
		}
	}

	for i := range tests {
		fmt.Println(tests[i].duration)
	}

	return nil
}
