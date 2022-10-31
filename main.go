package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	flagPath          = flag.String("p", "/metrics", "HTTP path where to expose metrics to")
	flagListen        = flag.String("l", ":9106", "Address to listen to")
	flagAPIHost       = flag.String("A", "", "URL of the PUN API endpoint")
	flagSleepInterval = flag.Duration("i", time.Minute, "Interval between speedtest executions, expressed as a Go duration string")
)

func main() {
	flag.Parse()

	punGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mercatoelettrico_pun",
			Help: "PUN - Prezzo Unico Nazionale for the Italian Mercato Elettrico",
		},
		[]string{},
	)
	if err := prometheus.Register(punGauge); err != nil {
		log.Fatalf("Failed to register PUN gauge: %v", err)
	}

	go func() {
		firstrun := true
		for {
			if !firstrun {
				log.Printf("Sleeping %s...", *flagSleepInterval)
				time.Sleep(*flagSleepInterval)
			}
			firstrun = false
			log.Printf("Fetching PUN value...")
			resp, err := http.Get(*flagAPIHost)
			if err != nil {
				log.Printf("Failed to get PUN value: %v", err)
				continue
			}
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				continue
			}
			if err := resp.Body.Close(); err != nil {
				log.Printf("Warning: failed to close HTTP body: %v", err)
			}
			v, err := strconv.ParseFloat(string(data), 64)
			if err != nil {
				continue
			}
			punGauge.WithLabelValues().Set(v)
		}
	}()

	http.Handle(*flagPath, promhttp.Handler())
	log.Printf("Starting server on %s", *flagListen)
	log.Fatal(http.ListenAndServe(*flagListen, nil))
}
