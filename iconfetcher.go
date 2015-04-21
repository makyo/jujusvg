package jujusvg

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"

	"github.com/juju/utils/parallel"
	"github.com/juju/xml"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v5"
)

// An IconFetcher provides functionality for retrieving icons for the charms
// within a given bundle.  The FetchIcons function accepts a bundle, and
// returns a map from charm paths to icon data.
type IconFetcher interface {
	FetchIcons(*charm.BundleData) (map[string][]byte, error)
}

// LinkFetcher fetches icons as links so that they are included within the SVG
// as remote resources.
type LinkFetcher struct {
	// IconURL returns the URL of the entity for embedding
	IconURL func(*charm.Reference) string
}

// FetchIcons generates the svg image tags given an appropriate URL, generating
// tags only for unique icons.
func (l *LinkFetcher) FetchIcons(b *charm.BundleData) (map[string][]byte, error) {
	// Maintain a list of icons that have already been fetched.
	alreadyFetched := make(map[string]bool)

	// Build the map of icons.
	icons := make(map[string][]byte)
	for _, serviceData := range b.Services {
		charmId, err := charm.ParseReference(serviceData.Charm)
		if err != nil {
			return nil, errgo.Notef(err, "cannot parse charm %q", serviceData.Charm)
		}
		path := charmId.Path()

		// Don't duplicate icons in the map.
		if !alreadyFetched[path] {
			alreadyFetched[path] = true
			icons[path] = []byte(fmt.Sprintf(`
				<svg xmlns:xlink="http://www.w3.org/1999/xlink">
					<image width="96" height="96" xlink:href="%s" />
				</svg>`, escapeString(l.IconURL(charmId))))
		}
	}
	return icons, nil
}

// Wrap around xml.EscapeText to make it more string-friendly.
func escapeString(s string) string {
	var buf bytes.Buffer
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// HTTPFetcher is an implementation of IconFetcher which retrieves charm
// icons from the web using the URL generated by IconURL on that charm.  The
// HTTP Client used may be overridden by an instance of http.Client.  The icons
// may optionally be fetched concurrently.
type HTTPFetcher struct {
	// Concurrency specifies the number of GoRoutines to use when fetching
	// icons.  If it is not positive, 10 will be used.  Setting this to 1
	// makes this method nominally synchronous.
	Concurrency int

	// IconURL returns the URL from which to fetch the given entity's icon SVG.
	IconURL func(*charm.Reference) string

	// Client specifies what HTTP client to use; if it is not provided,
	// http.DefaultClient will be used.
	Client *http.Client
}

// FetchIcons retrieves icon SVGs over HTTP.  If specified in the struct, icons
// will be fetched concurrently.
func (h *HTTPFetcher) FetchIcons(b *charm.BundleData) (map[string][]byte, error) {
	client := http.DefaultClient
	if h.Client != nil {
		client = h.Client
	}
	concurrency := h.Concurrency
	if concurrency <= 0 {
		concurrency = 10
	}
	var iconsMu sync.Mutex // Guards icons.
	icons := make(map[string][]byte)
	alreadyFetched := make(map[string]bool)
	run := parallel.NewRun(concurrency)
	for _, serviceData := range b.Services {
		charmId, err := charm.ParseReference(serviceData.Charm)
		if err != nil {
			return nil, errgo.Notef(err, "cannot parse charm %q", serviceData.Charm)
		}
		path := charmId.Path()
		if alreadyFetched[path] {
			continue
		}
		alreadyFetched[path] = true
		run.Do(func() error {
			icon, err := h.fetchIcon(h.IconURL(charmId), client)
			if err != nil {
				return err
			}
			iconsMu.Lock()
			defer iconsMu.Unlock()
			icons[path] = icon
			return nil
		})
	}
	if err := run.Wait(); err != nil {
		return nil, err
	}
	return icons, nil
}

// fetchIcon retrieves a single icon svg over HTTP.
func (h *HTTPFetcher) fetchIcon(url string, client *http.Client) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, errgo.Notef(err, "HTTP error fetching %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errgo.Newf("cannot retrieve icon from %s: %s", url, resp.Status)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errgo.Notef(err, "could not read icon data from url %s", url)
	}
	return body, nil
}
