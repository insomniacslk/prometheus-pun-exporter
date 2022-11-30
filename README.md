# prometheus-pun-exporter

This is a prometheus exporter for PUN values. PUN is Prezzo Unico Nazionale, the single national price for electricity in Italy.
This exporter requires the companion service [`punapi`](tools/punapi), that gets the PUN information from mercatoelettrico.org's
XML files. `punapi` requires Chrome headless, so you may want to run it on a different host than the exporter.

It exports one metric:
* `mercatoelettrico_pun`, a gauge with the value of the hour for one MWh of electricity
* `mercatoelettrico_pun_monthly_average`, a gauge with the monthly average of all the PUN values of the requested month

## Run it

```
go build
./prometheus-pun-exporter -A http://your-punapi-endpoint
```
