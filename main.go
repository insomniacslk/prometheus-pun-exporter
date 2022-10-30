package main

import (
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/spf13/pflag"
)

const progname = "powercost"

// curl -q "https://prometheus.cicciogatto.lol/api/v1/query?query=sum(tapo_plug_power_usage_today)&time=$(date -d '2022-10-26 23:59 CEST' +%s)" | jq -r '.data.result[0].value[1]'

var (
	flagPricePerKwh        = pflag.Float64P("price-per-kwh", "p", 0, "Price per kWh")
	flagCurrency           = pflag.StringP("currency", "C", defaultCurrency, "Currency name")
	flagTime               = pflag.StringP("time", "t", "", "Time string for the point in time the consumption is desired. Format: YYYY-MM-DD hh:mm:ss. If hh:mm:ss is omitted, use current time for today, or 23:59:00 for past days. All times are local time.")
	flagCustomQuery        = pflag.StringP("custom-query", "q", "", "Use custom query instead of presets")
	flagPrometheusQueryURL = pflag.StringP("prometheus-host-port", "P", defaultPrometheusQueryURL.String(), "Prometheus query URL")
)

func parseTime(s string) (*time.Time, error) {
	now := time.Now()
	if s == "" {
		return &now, nil
	}
	t, err := time.Parse("2006-02-01 15:04:00", s)
	if err != nil {
		t, err = time.Parse("2006-02-01", s)
		if err != nil {
			return nil, fmt.Errorf("wrong format, want yyyy-mm-dd [hh:mm:ss]: %w", err)
		}
		// if only the day is specified, also add hour, minute, second.
		yn, mn, dn := now.Date()
		y, m, d := t.Date()
		if y == yn && m == mn && d == dn {
			// if it's today, return `now`
			return &now, nil
		}
		// otherwise return the last minute of that day
		t = time.Date(y, m, d, 23, 59, 00, 0, now.Location())
		return &t, nil
	}

	return nil, fmt.Errorf("not implemented yet")
}

func usageSummary(cfg *Config, q string, t *time.Time, title string, pricePerKwh float64) error {
	watts, err := promQueryAt(cfg, q, t)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	fmt.Printf("## %s\n", title)
	fmt.Printf("    query: %s\n", q)
	fmt.Printf("    usage: %d W\n", int(watts))
	cost := float64(watts) * pricePerKwh / 1000
	fmt.Printf("    cost : %.3f %s\n", cost, cfg.Currency)
	return nil
}

func getOverrides() map[string]interface{} {
	overrides := make(map[string]interface{}, 0)
	if *flagCurrency != defaultCurrency {
		overrides["currency"] = *flagCurrency
	}
	if *flagPrometheusQueryURL != defaultPrometheusQueryURL.String() {
		promURL, err := url.Parse(*flagPrometheusQueryURL)
		if err != nil {
			log.Fatalf("Invalid prometheus query URL")
		}
		if promURL.Scheme != defaultPrometheusScheme {
			overrides["prometheus_scheme"] = promURL.Scheme
		}
		if promURL.Host != defaultPrometheusHostPort {
			overrides["prometheus_host_port"] = promURL.Host
		}
		if promURL.Path != defaultPrometheusQueryPath {
			overrides["prometheus_query_path"] = promURL.Path
		}
	}
	return overrides
}

func main() {
	pflag.Parse()
	if *flagPricePerKwh == 0 {
		log.Fatalf("Error: price per kWh is required")
	}
	t, err := parseTime(*flagTime)
	if err != nil {
		log.Fatalf("Error: invalid time: %v", err)
	}

	// load configuration with overrides, if any
	cfg, err := loadConfig(getOverrides())
	if err != nil {
		log.Fatalf("Error: cannot load configuration: %v", err)
	}
	fmt.Printf("Loaded config file '%s'\n", cfg.path)

	if *flagCustomQuery != "" {
		if err := usageSummary(cfg, *flagCustomQuery, t, "Custom query", *flagPricePerKwh); err != nil {
			log.Fatalf("Cannot get today's usage: %v", err)
		}
	} else {
		if err := usageSummary(cfg, "sum(tapo_plug_power_usage_today)", t, "Today", *flagPricePerKwh); err != nil {
			log.Fatalf("Cannot get today's usage: %v", err)
		}
		if err := usageSummary(cfg, "sum(tapo_plug_power_usage_past7)", t, "Past 7 days", *flagPricePerKwh); err != nil {
			log.Fatalf("Cannot get past 7 days usage: %v", err)
		}
		if err := usageSummary(cfg, "sum(tapo_plug_power_usage_past30)", t, "Past 30 days", *flagPricePerKwh); err != nil {
			log.Fatalf("Cannot get past 30 days usage: %v", err)
		}
	}
}
