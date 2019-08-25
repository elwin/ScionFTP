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

type test struct {
	mode        byte
	parallelism int
	payload     int // in MB
	blockSize   int
	duration    time.Duration
}

func (test *test) String() string {
	if test.mode == mode.Stream {
		return fmt.Sprintf("Stream with %d MB: %s", test.payload, test.duration)
	} else {
		return fmt.Sprintf("Extended (streams: %d, bs: %d) with %d MB: %s", test.parallelism, test.blockSize, test.payload, test.duration)
	}
}

func run() error {

	extended := []rune{mode.Stream, mode.ExtendedBlockMode}
	parallelisms := []int{1, 2, 4, 8, 16, 32}
	payloads := []int{8}
	blocksizes := []int{512, 1024, 2048, 4096}

	var tests []*test
	for _, m := range extended {
		for _, payload := range payloads {
			if m == mode.Stream {
				test := &test{
					mode:    mode.Stream,
					payload: payload,
				}
				tests = append(tests, test)
			} else {
				for _, blocksize := range blocksizes {
					for _, parallelism := range parallelisms {
						test := &test{
							mode:        mode.ExtendedBlockMode,
							parallelism: parallelism,
							payload:     payload,
							blockSize:   blocksize,
						}
						tests = append(tests, test)
					}
				}
			}
		}
	}

	conn, err := ftp.Dial(remote)
	if err != nil {
		return err
	}

	if err = conn.Login("admin", "123456"); err != nil {
		return err
	}

	for _, test := range tests {
		err = conn.Mode(test.mode)
		if err != nil {
			return err
		}

		if test.mode == mode.ExtendedBlockMode {
			err = conn.SetRetrOpts(test.parallelism, test.blockSize)
			if err != nil {
				return err
			}
		}

		start := time.Now()
		response, err := conn.Retr(strconv.Itoa(test.payload * 1024 * 1024))
		if err != nil {
			return err
		}

		n, err := io.Copy(ioutil.Discard, response)
		if err != nil {
			return err
		}
		if int(n) != test.payload*1024*1024 {
			return fmt.Errorf("failed to read correct number of bytes, expected %d but got %d", test.payload*1024*1024, n)
		}
		response.Close()

		test.duration += time.Since(start)
		fmt.Print(".")
	}
	fmt.Println()

	for _, test := range tests {
		fmt.Println(test)
	}

	return nil
}
