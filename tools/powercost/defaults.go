package main

import "net/url"

const (
	defaultPrometheusScheme    = "https"
	defaultPrometheusHostPort  = "localhost"
	defaultPrometheusQueryPath = "/api/v1/query"
	defaultCurrency            = "EUR"
)

var defaultPrometheusQueryURL = url.URL{
	Scheme: defaultPrometheusScheme,
	Host:   defaultPrometheusHostPort,
	Path:   defaultPrometheusQueryPath,
}
