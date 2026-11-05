// Package metrics registers Prometheus collectors scraped via the health
// server's /metrics endpoint. Label cardinality stays low (load name, status,
// target); never label by run UUID or row id.
package metrics
