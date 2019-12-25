package crawler

import (
	"io/ioutil"
	"log"
	"testing"
)

func BenchmarkCrawl(b *testing.B) {
	var errWriter = ioutil.Discard
	for i := 0; i < b.N; i++ {
		crawler := Crawler{
			Sites:  []string{
				"https://www.scalyr.com/",
				//"https://techbeacon.com/",
			},
			Get:    nil,
			Output: ioutil.Discard,
			Logger: log.New(errWriter, "", 0),
		}
		_ = crawler.Run()
	}
}

