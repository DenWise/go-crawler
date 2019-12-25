package main

import (
	"fmt"
	crawler2 "github.com/denwise/go-crawler/pkg/crawler"
	"io"
	"log"
	"os"
)

func main() {

	var (
		errWriter io.Writer = os.Stderr
		output    io.Writer = os.Stdout
	)

	crawler := crawler2.Crawler{
		Sites:  []string{
			"https://www.scalyr.com/",
		},
		Get:    nil,
		Output: output,
		Logger: log.New(errWriter, "", 0),
	}
	err := crawler.Run()

	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to run crawler: %v", err)
		os.Exit(1)
	}
}
