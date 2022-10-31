# powercost

Small CLI to print the power usage and cost of the current day, past 7 days, and
past 30 days, using the metrics exported on Prometheus by
[insomniacslk/prometheus-tapo-exporter](https://github.com/insomniacslk/prometheus-tapo-exporter).

## Configuration

A configuration file is automatically created upon the first run:

```
$ go run . -p 0.5
2022/10/30 11:14:05 Created default configuration file at '/home/insomniac/.config/powercost/config.json'
Loaded config file '/home/insomniac/.config/powercost/config.json'
2022/10/30 11:14:05 Cannot get today's usage: query failed: http.GET failed: Get "https://localhost/api/v1/query?query=sum%28tapo_plug_power_usage_today%29&time=1667124845": dial tcp [::1]:443: connect: connection refused
exit status 1
```

It might fail with the default parametrs. Modify it with your own parameters, then re-run it.

## Sample output

```
Loaded config file '/home/insomniac/.config/powercost/config.json'
## Today
    query: sum(tapo_plug_power_usage_today)
    usage: 1708 W
    cost : 0.854 EUR
## Past 7 days
    query: sum(tapo_plug_power_usage_past7)
    usage: 28302 W
    cost : 14.151 EUR
## Past 30 days
    query: sum(tapo_plug_power_usage_past30)
    usage: 29473 W
    cost : 14.736 EUR
    ```
