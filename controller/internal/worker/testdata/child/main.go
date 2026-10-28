package main

import (
	"bufio"
	"os"
	"strings"
	"time"
)

func main() {
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "--user-data-dir=") {
			for {
				time.Sleep(time.Second)
			}
		}
	}
	s := bufio.NewScanner(os.Stdin)
	for s.Scan() {
		if s.Text() == "stop" {
			return
		}
	}
}
