package main

import (
	"io/ioutil"
	"log"
	"os"
	"testing"
)

func BenchmarkCrawl(b *testing.B) {
	f, err := os.OpenFile("logfile.log", os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		log.Fatalf("error opening file: %v", err)
	}
	_ = f.Truncate(0)
	defer f.Close()
	for i := 0; i < b.N; i++ {
		crawler := Crawler{
			Sites:  []string{
				"https://www.scalyr.com/",
				"https://inhalefilms.com/",
				"https://hugagency.com/",
				"https://opentr.ca/",
				"https://cinetic.ca/",
			},
			Output: ioutil.Discard,
			Logger: log.New(f, "", log.LstdFlags|log.Llongfile),
		}
		_ = crawler.Run()
	}
}

