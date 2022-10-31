package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type promDatapoint struct {
	Timestamp float64
	Value     string
}

func (d *promDatapoint) MarshalJSON() ([]byte, error) {
	return json.Marshal([]interface{}{d.Timestamp, d.Value})
}

func (d *promDatapoint) UnmarshalJSON(b []byte) error {
	dp := []interface{}{}
	if err := json.Unmarshal(b, &dp); err != nil {
		return err
	}
	if len(dp) != 2 {
		return fmt.Errorf("promDatapoint: expected 2 values, got %d", len(dp))
	}
	d.Timestamp = dp[0].(float64)
	d.Value = dp[1].(string)
	return nil
}

func promQueryAt(cfg *Config, qs string, t *time.Time) (float64, error) {
	// if no time is specified, let it be now
	if t == nil {
		n := time.Now()
		t = &n
	}
	u := url.URL{
		Scheme: cfg.PrometheusScheme,
		Host:   cfg.PrometheusHostPort,
		Path:   cfg.PrometheusQueryPath,
	}
	q := u.Query()
	q.Set("query", qs)
	q.Set("time", strconv.FormatInt(t.Unix(), 10))
	u.RawQuery = q.Encode()
	resp, err := http.Get(u.String())
	if err != nil {
		return 0, fmt.Errorf("http.GET failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("received non-200 HTTP code: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("io.ReadAll failed: %w", err)
	}
	var j promResponse
	if err := json.Unmarshal(body, &j); err != nil {
		return 0, fmt.Errorf("json.Unmarshal failed: %w", err)
	}
	f, err := strconv.ParseFloat(j.Data.Result[0].Value.Value, 64)
	if err != nil {
		return 0, fmt.Errorf("strconv.ParseFloat64 failed: %w", err)
	}
	return f, nil
}

type promResponse struct {
	Status string
	Data   struct {
		ResultType string
		Result     []struct {
			Metric interface{}
			Value  promDatapoint
		}
	}
}
