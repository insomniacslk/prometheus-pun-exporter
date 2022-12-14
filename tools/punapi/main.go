// retrieve the PUN - Prezzo Unico Nazionale from MercatoElettrico.org's XML files.
package main

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"

	"github.com/spf13/pflag"
)

const (
	progname = "punapi"
	startURL = "https://www.mercatoelettrico.org/En/Tools/Accessodati.aspx?ReturnUrl=%2fEn%2fDownload%2fDownloadDati.aspx%3fval%3dMGP_Prezzi&val=MGP_Prezzi"
)

var (
	flagDebug         = pflag.BoolP("debug", "d", false, "Enable debug log")
	flagShowBrowser   = pflag.BoolP("show-browser", "b", false, "show browser, useful for debugging")
	flagChromePath    = pflag.StringP("chrome-path", "C", "", "Custom path for chrome browser")
	flagProxy         = pflag.StringP("proxy", "P", "", "HTTP proxy")
	flagTimeout       = pflag.DurationP("timeout", "t", 2*time.Minute, "Global timeout as a parsable duration (e.g. 1h12m)")
	flagDisableGPU    = pflag.BoolP("disable-gpu", "g", false, "Pass --disable-gpu to chrome")
	flagListenAddress = pflag.StringP("listen-address", "l", ":8080", "HTTP listen address")
)

func getTimeFromQuery(w http.ResponseWriter, r *http.Request) *time.Time {
	t := time.Now()
	var err error
	ts := r.URL.Query().Get("time")
	if ts != "" {
		t, err = time.Parse("2006-01-02 15:04", ts)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Time parameter format must be yyyy-mm-dd hh:mm"))
			return nil
		}
	}
	return &t
}

func makeMonthHandler(cache *Cache, timeout time.Duration, showBrowser bool, doDebug bool, chromePath string, proxy string, disableGPU bool) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		t := getTimeFromQuery(w, r)
		if t == nil {
			return
		}
		year, month, _ := t.Date()
		k := fmt.Sprintf("%d-%d", year, month)
		loc := time.Now().Location()
		firstDay := time.Date(year, month, 1, 0, 0, 0, 0, loc)
		lastDay := firstDay.AddDate(0, 1, -1)
		log.Printf("from %s to %s", firstDay, lastDay)
		puns, ok := cache.Get(k)
		if !ok {
			log.Printf("Cache miss or expired for %s", k)
			ctx, cancelFuncs := WithCancel(context.Background(), timeout, showBrowser, doDebug, chromePath, proxy, disableGPU)
			for _, cancel := range cancelFuncs {
				defer cancel()
			}
			// FIXME lock concurrent use of Fetch
			v, err := Fetch(ctx, firstDay, lastDay)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(fmt.Sprintf("Fetch failed: %v", err)))
				return
			}
			cache.Put(k, v)
			puns = v
		}
		var (
			sum   float64
			count uint
		)
		for _, pun := range puns {
			for _, p := range pun.Prezzi {
				sum += float64(p.PUN)
				count++
			}
		}
		if count == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(fmt.Sprintf("No PUN found for %s", t)))
		}
		_, _ = w.Write([]byte(fmt.Sprintf("%.6f", sum/(float64(count)))))
	}
}

func makeHandler(cache *Cache, timeout time.Duration, showBrowser bool, doDebug bool, chromePath string, proxy string, disableGPU bool) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		t := getTimeFromQuery(w, r)
		if t == nil {
			return
		}
		// TODO ensure that time zones do not cause an off-by-one
		year, month, day := t.Date()
		// FIXME during DST changes there are days with 25 items (Ora == 25) and
		// days with 23 items (Ora == 23 but not 24). This case is not handled
		// yet
		k := fmt.Sprintf("%d-%d-%d", year, month, day)
		puns, ok := cache.Get(k)
		// cache miss if:
		// * the entry is not in the cache
		// * the entry has expired
		// * we are at the minute 0 of the hour (expecting an update of the PUN value)
		if !ok || time.Now().Minute() == 0 {
			log.Printf("Cache miss or expired for %s", k)
			ctx, cancelFuncs := WithCancel(context.Background(), timeout, showBrowser, doDebug, chromePath, proxy, disableGPU)
			for _, cancel := range cancelFuncs {
				defer cancel()
			}
			// FIXME lock concurrent use of Fetch
			v, err := Fetch(ctx, *t, *t)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(fmt.Sprintf("Fetch failed: %v", err)))
				return
			}
			cache.Put(k, v)
			puns = v
		}
		if len(puns) != 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(fmt.Sprintf("Want exactly 1 PUN, got %d", len(puns))))
			return
		}
		pun := puns[0]
		for _, p := range pun.Prezzi {
			// Ora starts at 1, Hour starts at 0
			if p.Ora == t.Hour()+1 {
				_, _ = w.Write([]byte(fmt.Sprintf("%.6f", p.PUN)))
				return
			}
		}
		// if we are here, no PUN was found for the requested hour
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(fmt.Sprintf("No PUN found for %s", t)))
	}
}

type CacheEntry struct {
	PUN []PUNXML
	Ts  time.Time
}

type Cache struct {
	entries map[string]*CacheEntry
	TTL     time.Duration
	mu      sync.Mutex
}

func (c *Cache) Get(k string) ([]PUNXML, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// TODO check if cache expired
	e, ok := c.entries[k]
	if ok {
		if time.Since(e.Ts) > c.TTL {
			return nil, false
		}
		return e.PUN, true
	}
	return nil, false
}

func (c *Cache) Put(k string, v []PUNXML) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[k] = &CacheEntry{
		PUN: v,
		Ts:  time.Now(),
	}
}

func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]*CacheEntry),
		TTL:     ttl,
	}
}

func main() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "%s: expose an HTTP API to retrieve the Prezzo Unico Nazionale from MercatoElettrico.org's data.\n\n", progname)
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		pflag.PrintDefaults()
		os.Exit(1)
	}
	pflag.Parse()

	// TODO make TTL configurable
	cache := NewCache(time.Hour)
	http.HandleFunc("/", makeHandler(cache, *flagTimeout, *flagShowBrowser, *flagDebug, *flagChromePath, *flagProxy, *flagDisableGPU))
	http.HandleFunc("/month", makeMonthHandler(cache, *flagTimeout, *flagShowBrowser, *flagDebug, *flagChromePath, *flagProxy, *flagDisableGPU))
	log.Printf("Listening on %s", *flagListenAddress)
	log.Fatal(http.ListenAndServe(*flagListenAddress, nil))
}

// Fetch the PUN data from mercatoelettrico.org for the provided date.
func Fetch(ctx context.Context, start, end time.Time) ([]PUNXML, error) {
	log.Printf("Fetching PUN XML from %s to %s", start.Format("2006-01-02"), end.Format("2006-01-02"))
	tasks := chromedp.Tasks{
		chromedp.Navigate(startURL),
	}
	acceptBox1 := `//*[@id="ContentPlaceHolder1_CBAccetto1"]`
	acceptBox2 := `//*[@id="ContentPlaceHolder1_CBAccetto2"]`
	acceptButton := `//*[@id="ContentPlaceHolder1_Button1"]`
	tasks = append(tasks,
		chromedp.WaitVisible(acceptBox1, chromedp.BySearch),
		chromedp.Click(acceptBox1),
		chromedp.WaitVisible(acceptBox2, chromedp.BySearch),
		chromedp.Click(acceptBox2),
		chromedp.WaitVisible(acceptButton, chromedp.BySearch),
		chromedp.Click(acceptButton),
	)
	done := make(chan string, 1)

	// add download listener
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		select {
		// TODO make timeout configurable
		case <-time.After(time.Minute):
			log.Printf("Download timed out")
			return
		default:
			if evt, ok := ev.(*browser.EventDownloadProgress); ok {
				completed := "(unknown)"
				if evt.TotalBytes != 0 {
					completed = fmt.Sprintf("%0.2f%%", evt.ReceivedBytes/evt.TotalBytes*100.0)
				}
				log.Printf("state: %s, completed: %s\n", evt.State.String(), completed)
				if evt.State == browser.DownloadProgressStateCompleted {
					done <- evt.GUID
					close(done)
				}
			}
		}
	})

	// download the zipped XML
	startDateInput := `//*[@id="ContentPlaceHolder1_tbDataStart"]`
	endDateInput := `//*[@id="ContentPlaceHolder1_tbDataStop"]`
	downloadButton := `//*[@id="ContentPlaceHolder1_btnScarica"]`
	loc := time.Now().Location()
	startDate := start.In(loc).Format("02/01/2006")
	endDate := end.In(loc).Format("02/01/2006")
	tmpdir, err := os.MkdirTemp("", progname)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	tasks = append(tasks,
		chromedp.WaitVisible(startDateInput, chromedp.BySearch),
		chromedp.SendKeys(startDateInput, startDate),
		chromedp.WaitVisible(endDateInput, chromedp.BySearch),
		chromedp.SendKeys(endDateInput, endDate),
		chromedp.WaitVisible(downloadButton, chromedp.BySearch),
		browser.SetDownloadBehavior(
			browser.SetDownloadBehaviorBehaviorAllowAndName).
			WithDownloadPath(tmpdir).
			WithEventsEnabled(true),
		chromedp.Click(downloadButton),
	)
	defer func() {
		log.Printf("Removing temporary directory '%s'", tmpdir)
		if err := os.RemoveAll(tmpdir); err != nil {
			log.Printf("Failed to remove temporary directory '%s': %v", tmpdir, err)
		}
	}()
	err = chromedp.Run(ctx, tasks)
	guid := <-done
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	zipfile := path.Join(tmpdir, guid)
	log.Printf("download finished. File name is '%s'", zipfile)
	puns, err := ZipToPUNs(zipfile)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze ZIP file: %w", err)
	}

	return puns, nil
}

type PUNXML struct {
	XMLName xml.Name `xml:"NewDataSet"`
	Prezzi  []struct {
		XMLName xml.Name `xml:"Prezzi"`
		Data    string
		Mercato string
		Ora     int
		PUN     Price `xml:"PUN"`
		NAT     Price `xml:"NAT"`
		CALA    Price `xml:"CALA"`
		CNOR    Price `xml:"CNOR"`
		CSUD    Price `xml:"CSUD"`
		NORD    Price `xml:"NORD"`
		SARD    Price `xml:"SARD"`
		SICI    Price `xml:"SICI"`
		SUD     Price `xml:"SUD"`
		AUST    Price `xml:"AUST"`
		COAC    Price `xml:"COAC"`
		COUP    Price `xml:"COUP"`
		CORS    Price `xml:"CORS"`
		FRAN    Price `xml:"FRAN"`
		GREC    Price `xml:"GREC"`
		SLOV    Price `xml:"SLOV"`
		SVIZ    Price `xml:"SVIZ"`
		BSP     Price `xml:"BSP"`
		MALT    Price `xml:"MALT"`
		XAUS    Price `xml:"XAUS"`
		XFRA    Price `xml:"XFRA"`
		MONT    Price `xml:"MONT"`
		XGRE    Price `xml:"XGRE"`
	}
}

// warning: float64 is not suitable for prices if you need absolute
// accuracy. This is not a finance application.
type Price float64

func (p Price) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	rawValue := strings.Replace(
		// the original string has 6 significant decimal digits
		strconv.FormatFloat(float64(p), 'g', 6, 64),
		".",
		",",
		1,
	)
	return e.EncodeElement(rawValue, start)
}

func (p *Price) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var fs string
	if err := d.DecodeElement(&fs, &start); err != nil {
		return fmt.Errorf("failed to decode element: %w", err)
	}
	fs = strings.Replace(fs, ",", ".", -1)
	f, err := strconv.ParseFloat(fs, 64)
	if err != nil {
		return fmt.Errorf("strconv.ParseFloat failed: %w", err)
	}
	*p = Price(f)
	return nil
}

func ZipToPUNs(zipfile string) ([]PUNXML, error) {
	// extract zip file
	archive, err := zip.OpenReader(zipfile)
	if err != nil {
		return nil, fmt.Errorf("failed to open ZIP file '%s': %w", zipfile, err)
	}
	var filelist []*zip.File
	for _, f := range archive.File {
		if !f.FileInfo().IsDir() && strings.HasSuffix(f.Name, ".xml") {
			filelist = append(filelist, f)
		}
	}
	if len(filelist) < 1 {
		return nil, fmt.Errorf("expected at least one XML file in ZIP archive, got %d", len(archive.File))
	}
	punlist := make([]PUNXML, 0)
	for _, file := range filelist {
		log.Printf("Parsing file %s", file.Name)
		// TODO parse every XML in filelist and return a list of PUNXML
		fd, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("failed to open XML file '%s' contained in ZIP file: %w", filelist[0].Name, err)
		}
		defer func() {
			if err := fd.Close(); err != nil {
				log.Printf("Failed to close XML file '%s' contained in ZIP file: %v", filelist[0].Name, err)
			}
		}()
		data, err := io.ReadAll(fd)
		if err != nil {
			return nil, fmt.Errorf("failed to read XML file '%s' contained in ZIP file: %w", filelist[0].Name, err)
		}
		var pun PUNXML
		if err := xml.Unmarshal(data, &pun); err != nil {
			return nil, fmt.Errorf("failed to unmarshal XML: %w", err)
		}
		punlist = append(punlist, pun)
	}
	return punlist, nil
}

// WithCancel returns a chromedp context with a cancellation function.
func WithCancel(ctx context.Context, timeout time.Duration, showBrowser, doDebug bool, chromePath, proxyURL string, disableGPU bool) (context.Context, []func()) {
	var cancelFuncs []func()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	cancelFuncs = append(cancelFuncs, cancel)

	// show browser
	var allocatorOpts []chromedp.ExecAllocatorOption
	if showBrowser {
		allocatorOpts = append(allocatorOpts, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck)
	} else {
		allocatorOpts = append(allocatorOpts, chromedp.Headless)
	}
	if chromePath != "" {
		allocatorOpts = append(allocatorOpts, chromedp.ExecPath(chromePath))
	}
	if proxyURL != "" {
		allocatorOpts = append(allocatorOpts, chromedp.ProxyServer(proxyURL))
	}
	if disableGPU {
		allocatorOpts = append(allocatorOpts, chromedp.Flag("disable-gpu", disableGPU))
	}
	ctx, cancel = chromedp.NewExecAllocator(ctx, allocatorOpts...)
	cancelFuncs = append(cancelFuncs, cancel)

	var opts []chromedp.ContextOption
	if doDebug {
		opts = append(opts, chromedp.WithDebugf(log.Printf))
	}

	ctx, cancel = chromedp.NewContext(ctx, opts...)
	cancelFuncs = append(cancelFuncs, cancel)
	return ctx, cancelFuncs
}
