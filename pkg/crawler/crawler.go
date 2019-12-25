package crawler

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

const (
	defaultParallel       = 10
	wwwPrefix             = "www."
	protocolHostSeparator = "://"
)

var (
	reLinks = regexp.MustCompile(`<a.*?href="([^"]*)".*?>`)
	reTitle = regexp.MustCompile(`<title.*?>(.*)</title>`)
)

type Crawler struct {
	Sites  []string
	Get    func(string) (*http.Response, error)
	Output io.Writer
	Logger *log.Logger
}

type site struct {
	URL    *url.URL
	Parent *url.URL
}

func (c *Crawler) Run() error {

	if err := c.validateCrawler(); err != nil {
		return err
	}

	if c.Get == nil {
		c.Get = http.Get
	}

	urls, err := validateSites(c.Sites, url.Parse)
	if err != nil {
		return err
	}

	results := make(chan string)
	defer close(results)

	go func() {
		for r := range results {
			if _, err := fmt.Fprintln(c.Output, r); err != nil {
				c.Logger.Printf("error while writing output '%s': %v\n", r, err)
			}
		}
	}()

	queue, sites, wait := makeQueue()

	wait <- len(urls)

	var wg sync.WaitGroup
	for i := 0; i < defaultParallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.worker(sites, queue, wait, results)
		}()
	}

	for i := range urls {
		queue <- site{
			URL:    urls[i],
			Parent: nil,
		}
	}

	wg.Wait()

	return nil
}

func (c *Crawler) worker(sites <-chan site, queue chan<- site, wait chan<- int, results chan<- string) {
	for s := range sites {

		siteBody, err := crawlSiteBody(s, c.Get)

		if err != nil {
			c.Logger.Printf("%v : %s\n", err, s.URL.String())
			wait <- -1
			continue
		}

		title, externals, internals := getData(s.URL, siteBody)

		results <- fmt.Sprintf("%v : %v -> title: %s, internals: %v, externals: %v",
			s.Parent,
			s.URL.String(),
			title,
			len(internals),
			len(externals))

		urls, err := validateSites(internals, url.Parse)
		if err != nil {
			c.Logger.Printf("page %v: %v\n", s.URL, err)
		}

		wait <- len(urls) - 1

		go queueURLs(queue, urls, s.URL)
	}
}

func (c Crawler) validateCrawler() error {
	if len(c.Sites) == 0 {
		return errors.New("no sites given")
	}
	if c.Output == nil {
		return errors.New("output writer not defined")
	}
	if c.Logger == nil {
		return errors.New("logger not defined")
	}
	return nil
}

func validateSites(sites []string, parse func(string) (*url.URL, error)) (urls []*url.URL, err error) {

	var invalidUrls []string

	for i := range sites {
		u, e := parse(sites[i])
		if e != nil {
			invalidUrls = append(invalidUrls, fmt.Sprintf("%s (%v)", sites[i], e))
			continue
		}

		if u.Scheme == "http" || u.Scheme == "https" {
			urls = append(urls, u)
		}
	}

	if len(invalidUrls) > 0 {
		err = fmt.Errorf("invalid URLs: %v", strings.Join(invalidUrls, ", "))
	}

	return
}

func makeQueue() (chan<- site, <-chan site, chan<- int) {
	queueCount := 0
	wait := make(chan int)
	sites := make(chan site)
	queue := make(chan site)
	visited := map[string]struct{}{}

	go func() {
		for delta := range wait {
			queueCount += delta
			if queueCount == 0 {
				close(queue)
			}
		}
	}()

	go func() {
		for s := range queue {
			u := s.URL.String()
			if _, exists := visited[u]; !exists {
				visited[u] = struct{}{}
				sites <- s
			} else {
				wait <- -1
			}
		}
		close(sites)
		close(wait)
	}()

	return queue, sites, wait
}

func queueURLs(queue chan<- site, urls []*url.URL, parent *url.URL) {
	for i := range urls {
		queue <- site{
			URL:    urls[i],
			Parent: parent,
		}
	}
}

func crawlSiteBody(s site, get func(string) (*http.Response, error)) (body string, err error) {
	u := s.URL

	r, err := get(u.String())
	if err != nil {
		return "", fmt.Errorf("failed to get %v: %v\n", u, err)
	}
	defer r.Body.Close()

	if r.StatusCode >= 400 {
		return "", fmt.Errorf("%d : %v\n", r.StatusCode, u)
	}

	// Stop when redirecting to external page
	if r.Request.URL.Host != u.Host && wwwPrefix+r.Request.URL.Host != u.Host {
		return "", fmt.Errorf("stoped cause it's redirecting to external resource: %v -> %v\n", s.Parent, r.Request.URL.Host)
	}

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return "", fmt.Errorf("error while read body: %v\n", err)
	}
	bodyString := string(bodyBytes)

	return bodyString, nil
}

func getData(u *url.URL, body string) (title string, ext, int []string) {

	var (
		internals []string
		externals []string
	)
	titleSubmatch := reTitle.FindStringSubmatch(body)
	links := reLinks.FindAllStringSubmatch(body, -1)

	if len(links) > 0 {
		for i := range links {
			current := strings.TrimSpace(links[i][1])
			if strings.HasPrefix(current, "/") {
				internals = append(internals, u.Scheme+protocolHostSeparator+u.Host+current)
				continue
			}
			l, e := url.Parse(current)
			if e != nil || l.Host == "" {
				continue
			}
			if l.Host == u.Host || wwwPrefix+l.Host == u.Host {
				internals = append(internals, current)
			} else {
				externals = append(externals, current)
			}
		}
	}

	if len(titleSubmatch) == 2 {
		title = titleSubmatch[1]
	}

	return title, externals, internals
}
