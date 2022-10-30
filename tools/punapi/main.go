// retrieve my paid content from https://www.brunobarbieri.blog/barbieriplus-membri/
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
	flagDebug       = pflag.BoolP("debug", "d", false, "Enable debug log")
	flagShowBrowser = pflag.BoolP("show-browser", "b", false, "show browser, useful for debugging")
	flagChromePath  = pflag.StringP("chrome-path", "C", "", "Custom path for chrome browser")
	flagProxy       = pflag.StringP("proxy", "P", "", "HTTP proxy")
	flagTimeout     = pflag.DurationP("timeout", "t", 2*time.Minute, "Global timeout as a parsable duration (e.g. 1h12m)")
	flagDisableGPU  = pflag.BoolP("disable-gpu", "g", false, "Pass --disable-gpu to chrome")
)

func makeHandler(cache map[string]*PUNXML, timeout time.Duration, showBrowser bool, doDebug bool, chromePath string, proxy string, disableGPU bool) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		ts := r.URL.Query().Get("time")
		if ts == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Time parameter is not set"))
			return
		}
		t, err := time.Parse("2006-01-02 15:04", ts)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Time parameter format must be yyyy-mm-dd hh:mm"))
			return
		}
		// TODO ensure that time zones do not cause an off-by-one
		year, month, day := t.Date()
		// TODO if fetching today's data, ensure that it's fresh (i.e. that
		// there is new data for the requested hour)
		// FIXME during DST changes there are days with 25 items (Ora == 25) and
		// days with 23 items (Ora == 23 but not 24). This case is not handled
		// yet
		k := fmt.Sprintf("%d-%d-%d", year, month, day)
		puns, ok := cache[k]
		if !ok {
			log.Printf("Cache miss for %s", k)

			ctx, cancelFuncs := WithCancel(context.Background(), timeout, showBrowser, doDebug, chromePath, proxy, disableGPU)
			for _, cancel := range cancelFuncs {
				defer cancel()
			}
			puns, err = Fetch(ctx, year, int(month), day)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(fmt.Sprintf("Fetch failed: %v", err)))
				return
			}
			cache[k] = puns
		}
		for _, p := range puns.Prezzi {
			// Ora starts at 1, Hour starts at 0
			if p.Ora == t.Hour()+1 {
				_, _ = w.Write([]byte(fmt.Sprintf("%.6f", p.PUN)))
				return
			}
		}
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(fmt.Sprintf("No PUN found for %s: %v", t, err)))
			return
		}
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

	cache := make(map[string]*PUNXML)
	http.HandleFunc("/", makeHandler(cache, *flagTimeout, *flagShowBrowser, *flagDebug, *flagChromePath, *flagProxy, *flagDisableGPU))
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Fetch the PUN data from mercatoelettrico.org for the provided date.
func Fetch(ctx context.Context, year, month, day int) (*PUNXML, error) {
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
	startDate := time.Date(year, time.Month(month), day, 0, 0, 0, 0, loc).Format("02/01/2006")
	endDate := startDate
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

func ZipToPUNs(zipfile string) (*PUNXML, error) {
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
	if len(filelist) != 1 {
		return nil, fmt.Errorf("expected 1 XML file in ZIP archive, got %d", len(archive.File))
	}
	fd, err := filelist[0].Open()
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
	return &pun, nil
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
