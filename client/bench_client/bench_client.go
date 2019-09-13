package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"time"

	ftp "github.com/elwin/transmit2/client"

	"github.com/elwin/transmit2/mode"
)

var (
	remote = flag.String("remote", "", "Remote host (including port)")
)

const (
	sizeUnit = 1000 * 1000 // MB
	sleep    = 5 * time.Second
)

func main() {

	flag.Parse()
	if *remote == "" {
		log.Fatal("Please provide a remote address with -remote")
	}

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
		return fmt.Sprintf("Extended (streams: %d, bs: %d) with %d KB: %s", test.parallelism, test.blockSize, test.payload, test.duration)
	}
}

func writeToCsv(results []*test) {
	w := csv.NewWriter(os.Stderr)
	header := []string{"mode", "parallelism", "payload (KB)", "block_size", "duration"}
	if err := w.Write(header); err != nil {
		log.Fatal(err)
	}
	for _, result := range results {
		record := []string{
			string(result.mode),
			strconv.Itoa(result.parallelism),
			strconv.Itoa(result.payload),
			strconv.Itoa(result.blockSize),
			strconv.FormatFloat(result.duration.Seconds(), 'f', -1, 64),
		}
		if err := w.Write(record); err != nil {
			log.Fatal(err)
		}
	}

	w.Flush()
}

func run() error {

	extended := []rune{mode.ExtendedBlockMode, mode.Stream}
	parallelisms := []int{1, 2, 4, 8, 16, 32}
	payloads := []int{32}
	blocksizes := []int{4096}
	// blocksizes := []int{512, 1024, 2048, 4096, 8192}

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

	fmt.Printf("%d Testcases\n", len(tests))

	conn, err := ftp.Dial(*remote)
	defer conn.Quit()
	if err = conn.Login("admin", "123456"); err != nil {
		return err
	}

	// Warm-up
	if err = conn.Mode(mode.ExtendedBlockMode); err != nil {
		return err
	}
	if err = conn.SetRetrOpts(8, 4096); err != nil {
		return err
	}

	response, err := conn.Retr(strconv.Itoa(tests[0].payload * sizeUnit))
	if err != nil {
		log.Fatal("failed to retrieve file", err)
	} else {
		_, err = io.Copy(ioutil.Discard, response)
		if err != nil {
			log.Fatal("failed to copy data", err)
		}
		response.Close()
	}

	for _, test := range tests {
		time.Sleep(sleep)

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
		response, err := conn.Retr(strconv.Itoa(test.payload * sizeUnit))
		if err != nil {
			return err
		}

		n, err := io.Copy(ioutil.Discard, response)
		if err != nil {
			return err
		}
		if int(n) != test.payload*sizeUnit {
			return fmt.Errorf("failed to read correct number of bytes, expected %d but got %d", test.payload*sizeUnit, n)
		}
		response.Close()

		test.duration += time.Since(start)

		fmt.Print(".")
	}

	fmt.Println()

	fmt.Println("--------------")

	writeToCsv(tests)

	return nil
}
