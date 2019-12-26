package crawler

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"unsafe"
)

const (
	wwwPrefix             = "www."
	protocolHostSeparator = "://"
)

var (
	reLinks = regexp.MustCompile(`<a.*?href="([^"]*)".*?>`)
	reTitle = regexp.MustCompile(`<title.*?>(.*)</title>`)
	sem     = make(chan int, 10)
)

type Crawler struct {
	Sites  []string
	Output io.Writer
	Logger *log.Logger
}

type siteCrawler struct {
	sem     chan int
	queue   chan<- site
	pages   chan site
	wait    chan<- int
	results chan string
	url     *url.URL
	get     func(string) (*http.Response, error)
	logger  *log.Logger
}

type site struct {
	URL    *url.URL
	Parent *url.URL
}

func (c *Crawler) Run() error {

	if err := c.validateCrawler(); err != nil {
		return err
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

	// ------------------------------
	var wg sync.WaitGroup

	for i := range urls {
		wg.Add(1)
		newSiteCrawler(urls[i], results).Start(&wg)
	}

	wg.Wait()
	// ------------------------------

	return nil
}

func (sc *siteCrawler) Start(wg *sync.WaitGroup) {

	defer wg.Done()
	sc.wait <- 1

	wg.Add(1)
	go func() {
		defer wg.Done()

		siteBody, err := crawlSiteBody(sc.url, sc.get)

		if err != nil {
			sc.wait <- -1
			return
		}

		urls, err := sc.handleSiteBody(siteBody)
		if err != nil {
			sc.logger.Printf("page %v: %v\n", sc.url, err)
		}

		sc.wait <- len(urls) - 1

		go queueURLs(sc.queue, urls, sc.url)
	}()

	wg.Add(1)

	// горутина которая читает из канала pages и запускает на каждую страницу воркер но не больше 10 воркеров на текущий домен
	go func() {
		defer wg.Done()
		for p := range sc.pages {
			siteBody, err := crawlSiteBody(p.URL, sc.get)

			if err != nil {
				sc.wait <- -1
				return
			}

			urls, err := sc.handleSiteBody(siteBody)
			if err != nil {
				sc.logger.Printf("page %v: %v\n", sc.url, err)
			}

			sc.wait <- len(urls) - 1

			go queueURLs(sc.queue, urls, p.URL)
		}
	}()
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

func (sc * siteCrawler) handleSiteBody(body string) (urls []*url.URL, err error) {

	title, externals, internals := getData(sc.url, body)

	sc.results <- fmt.Sprintf(" %v -> title: %s, internals: %v, externals: %v\n",
		sc.url.String(),
		title,
		len(internals),
		len(externals))

	urls, err = validateSites(internals, url.Parse)
	if err != nil {
		return nil, err
	}

	return urls, nil
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

func newSiteCrawler(site *url.URL, res chan string) *siteCrawler {
	queue, pages, wait := makeQueue()
	return &siteCrawler{
		sem:     make(chan int, 10),
		queue:   queue,
		pages:   pages,
		wait:    wait,
		results: res,
		url:     site,
		get:     http.Get,
		logger:  log.New(os.Stderr, "", 0),
	}
}

func makeQueue() (chan<- site, chan site, chan<- int) {
	queueCount := 0
	wait := make(chan int)
	pages := make(chan site)
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
		for p := range queue {
			u := p.URL.String()
			if _, exists := visited[u]; !exists {
				visited[u] = struct{}{}
				pages <- p
			} else {
				wait <- -1
			}
		}
		close(pages)
		close(wait)
	}()

	return queue, pages, wait
}

func queueURLs(queue chan<- site, urls []*url.URL, parent *url.URL) {
	for i := range urls {
		queue <- site{
			URL:    urls[i],
			Parent: parent,
		}
	}
}

func crawlSiteBody(u *url.URL, get func(string) (*http.Response, error)) (body string, err error) {
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
		return "", fmt.Errorf("stoped cause it's redirecting to external resource: -> %v\n", r.Request.URL.Host)
	}

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return "", fmt.Errorf("error while read body: %v\n", err)
	}
	bodyString := *(*string)(unsafe.Pointer(&bodyBytes))

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
