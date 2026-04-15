// cpu-burner is a tiny HTTP server that burns CPU on every request.
// It exposes a Prometheus histogram (http_request_duration_seconds) so the
// KubeIntent operator's default PromQL queries work out of the box.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var reqDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "http_request_duration_seconds",
	Help:    "Request latency distribution.",
	Buckets: prometheus.DefBuckets,
}, []string{"method", "path", "status"})

func init() { prometheus.MustRegister(reqDuration) }

func main() {
	workMS := 50
	if v := os.Getenv("WORK_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			workMS = n
		}
	}
	port := "8080"
	if v := os.Getenv("PORT"); v != "" {
		port = v
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Burn CPU by computing SHA-256 in a tight loop.
		deadline := start.Add(time.Duration(workMS) * time.Millisecond)
		data := []byte("kubeintent-cpu-burner")
		for time.Now().Before(deadline) {
			data = sha256.New().Sum(data)
		}

		elapsed := time.Since(start).Seconds()
		reqDuration.WithLabelValues(r.Method, "/", "200").Observe(elapsed)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"work_ms": workMS,
			"elapsed": fmt.Sprintf("%.1fms", elapsed*1000),
		})
	})

	http.Handle("/metrics", promhttp.Handler())

	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	fmt.Printf("cpu-burner listening on :%s (work=%dms)\n", port, workMS)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
