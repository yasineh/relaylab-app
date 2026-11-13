package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbe(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer ok.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer down.Close()
	if err := probe(ok.URL+"/healthz", time.Second); err != nil {
		t.Fatalf("healthy: %v", err)
	}
	if err := probe(down.URL+"/healthz", time.Second); err == nil {
		t.Fatal("503 reported healthy")
	}
	for _, bad := range []string{"https://127.0.0.1/healthz", "http://example.com/healthz", "http://10.0.0.1/", "::"} {
		if err := probe(bad, time.Second); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if runHealthcheck([]string{"http://127.0.0.1:1/healthz"}) != 1 {
		t.Fatal("closed port reported healthy")
	}
}
