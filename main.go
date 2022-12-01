package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maja42/goval"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	flagPath           = flag.String("p", "/metrics", "HTTP path where to expose metrics to")
	flagListen         = flag.String("l", ":9106", "Address to listen to")
	flagAPIURL         = flag.String("A", "http://localhost:8080", "URL of the PUN API endpoint")
	flagCompoundMetric = flag.String("C", "", "Custom metric. If empty, no custom metric is exported. A custom metric based on PUN or the monthly average. Example: \"monthly_cost=MPUN/1000+0.08\". You can use PUN (latest PUN) and MPUN (monthly average)")
	flagSleepInterval  = flag.Duration("i", time.Minute, "Interval between speedtest executions, expressed as a Go duration string")
)

func splitLabelExpression(labelExpression string) (string, string, error) {
	parts := strings.SplitN(labelExpression, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("expected 2 expression components, got %d", len(parts))
	}
	return parts[0], parts[1], nil
}

func main() {
	flag.Parse()

	if *flagAPIURL == "" {
		log.Fatal("API URL cannot be empty")
	}
	u, err := url.Parse(*flagAPIURL)
	if err != nil {
		log.Fatalf("Invalid API URL: %v", err)
	}
	if u.Scheme == "" || u.Host == "" {
		log.Fatalf("Scheme or host cannot be empty in API URL")
	}

	var (
		eval                     *goval.Evaluator
		custom_name, custom_expr string
	)
	if *flagCompoundMetric != "" {
		custom_name, custom_expr, err = splitLabelExpression(*flagCompoundMetric)
		if err != nil {
			log.Fatalf("Failed to split label from expression: %v", err)
		}
		eval = goval.NewEvaluator()
	}

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
	punMonthlyAvgGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mercatoelettrico_pun_month_average",
			Help: "PUN - Current month's average for Prezzo Unico Nazionale for the Italian Mercato Elettrico",
		},
		[]string{},
	)
	if err := prometheus.Register(punMonthlyAvgGauge); err != nil {
		log.Fatalf("Failed to register PUN monthly average gauge: %v", err)
	}
	var punCustomGauge *prometheus.GaugeVec
	if eval != nil {
		log.Printf("Creating custom gauge `%s` with formula `%s`", custom_name, custom_expr)
		punCustomGauge = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "mercatoelettrico_" + custom_name,
				Help: "PUN - Custom metric using Prezzo Unico Nazionale - formula: " + custom_expr,
			},
			[]string{},
		)
		if err := prometheus.Register(punCustomGauge); err != nil {
			log.Fatalf("Failed to register PUN custom gauge: %v", err)
		}
	}

	getPun := func(endpoint string) (float64, error) {
		resp, err := http.Get(endpoint)
		if err != nil {
			return 0, fmt.Errorf("GET failed: %w", err)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return 0, fmt.Errorf("HTTP body read failed: %w", err)
		}
		if err := resp.Body.Close(); err != nil {
			log.Printf("Warning: failed to close HTTP body: %v", err)
		}
		return strconv.ParseFloat(string(data), 64)
	}

	go func() {
		firstrun := true
		for {
			if !firstrun {
				log.Printf("Sleeping %s...", *flagSleepInterval)
				time.Sleep(*flagSleepInterval)
			}
			firstrun = false
			// export PUN
			log.Printf("Fetching PUN value...")
			pun, err := getPun(*flagAPIURL)
			if err != nil {
				log.Printf("Failed to fetch PUN value: %v", err)
			} else {
				punGauge.WithLabelValues().Set(pun)
			}
			// export monthly PUN average
			log.Printf("Fetching PUN monthly average value...")
			punavg, err := getPun(*flagAPIURL + "/month")
			if err != nil {
				log.Printf("Failed to fetch PUN monthly average value: %v", err)
			} else {
				punMonthlyAvgGauge.WithLabelValues().Set(punavg)
			}
			// export custom metric
			log.Printf("Computing custom metric `%s`", custom_name)
			variables := map[string]interface{}{
				"PUN":  pun,
				"MPUN": punavg,
			}
			custom_metric, err := eval.Evaluate(custom_expr, variables, nil)
			if err != nil {
				log.Printf("Failed to evaluate custom metric: %v", err)
			} else {
				punCustomGauge.WithLabelValues().Set(custom_metric.(float64))
			}
		}
	}()

	http.Handle(*flagPath, promhttp.Handler())
	log.Printf("Starting server on %s", *flagListen)
	log.Fatal(http.ListenAndServe(*flagListen, nil))
}
