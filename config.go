package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"

	"github.com/kirsle/configdir"
)

type Config struct {
	PrometheusScheme    string `json:"prometheus_scheme"`
	PrometheusHostPort  string `json:"prometheus_host_port"`
	PrometheusQueryPath string `json:"prometheus_query_path"`
	Currency            string `json:"currency"`
	// internal field
	path string
}

func loadConfig(overrides map[string]interface{}) (*Config, error) {
	configPath := configdir.LocalConfig(progname)
	configFile := path.Join(configPath, "config.json")
	cfg := Config{
		PrometheusScheme:    defaultPrometheusScheme,
		PrometheusHostPort:  defaultPrometheusHostPort,
		PrometheusQueryPath: defaultPrometheusQueryPath,
		Currency:            defaultCurrency,
		path:                configFile,
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			// create default configuration
			if err := configdir.MakePath(configPath); err != nil {
				return nil, fmt.Errorf("failed to create config path '%s': %w", configPath, err)
			}
			cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("failed to marshal configuration to JSON: %w", err)
			}
			if err := os.WriteFile(configFile, cfgBytes, 0644); err != nil {
				return nil, fmt.Errorf("failed to write configuration file '%s': %w", configFile, err)
			}
			log.Printf("Created default configuration file at '%s'", configFile)
		}
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal configuration: %w", err)
	}
	// apply overrides
	for k, v := range overrides {
		switch k {
		case "prometheus_scheme":
			cfg.PrometheusScheme = v.(string)
		case "prometheus_host_port":
			cfg.PrometheusHostPort = v.(string)
		case "prometheus_query_path":
			cfg.PrometheusQueryPath = v.(string)
		case "currency":
			cfg.Currency = v.(string)
		default:
			return nil, fmt.Errorf("unknown config override '%s'", k)
		}
	}
	return &cfg, nil
}
