package telemetry

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQuery_SingleElementVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"status": "success",
			"data": {
				"resultType": "vector",
				"result": [
					{
						"metric": {},
						"value": [1609459200, "0.042"]
					}
				]
			}
		}`))
	}))
	defer srv.Close()

	client, err := NewPrometheusClient(srv.URL)
	if err != nil {
		t.Fatalf("NewPrometheusClient: %v", err)
	}

	val, err := client.Query(context.Background(), "test_query")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if math.Abs(val-0.042) > 1e-9 {
		t.Errorf("expected 0.042, got %f", val)
	}
}

func TestQuery_EmptyVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"status": "success",
			"data": {
				"resultType": "vector",
				"result": []
			}
		}`))
	}))
	defer srv.Close()

	client, err := NewPrometheusClient(srv.URL)
	if err != nil {
		t.Fatalf("NewPrometheusClient: %v", err)
	}

	_, err = client.Query(context.Background(), "test_query")
	if err == nil {
		t.Fatal("expected error for empty vector, got nil")
	}
}

func TestQuery_MultiElementVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"status": "success",
			"data": {
				"resultType": "vector",
				"result": [
					{"metric": {"a": "1"}, "value": [1609459200, "1.0"]},
					{"metric": {"a": "2"}, "value": [1609459200, "2.0"]}
				]
			}
		}`))
	}))
	defer srv.Close()

	client, err := NewPrometheusClient(srv.URL)
	if err != nil {
		t.Fatalf("NewPrometheusClient: %v", err)
	}

	_, err = client.Query(context.Background(), "test_query")
	if err == nil {
		t.Fatal("expected error for multi-element vector, got nil")
	}
}
