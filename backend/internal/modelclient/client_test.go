package modelclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbe_HTMLResponseFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html><body>New API</body></html>"))
	}))
	defer srv.Close()

	c := New("custom", srv.URL, "test-key", 5*time.Second)
	res := c.Probe(context.Background())
	if res.OK {
		t.Fatalf("expected probe to fail for HTML response, got OK")
	}
	if !strings.Contains(res.Error, "HTML") {
		t.Fatalf("expected error mentioning HTML, got: %s", res.Error)
	}
}

func TestProbe_JSONResponseOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1","object":"model"}]}`))
	}))
	defer srv.Close()

	c := New("custom", srv.URL, "test-key", 5*time.Second)
	res := c.Probe(context.Background())
	if !res.OK {
		t.Fatalf("expected probe OK, got error: %s", res.Error)
	}
	if len(res.Models) != 1 || res.Models[0] != "m1" {
		t.Fatalf("expected model m1, got %v", res.Models)
	}
}

func TestChat_HTMLResponseFailsClearly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>spa</body></html>"))
	}))
	defer srv.Close()

	c := New("custom", srv.URL, "test-key", 5*time.Second)
	_, err := c.Chat(context.Background(), "m1", []ChatMessage{{Role: "user", Content: "hi"}}, nil, GenOptions{})
	if err == nil {
		t.Fatal("expected error for HTML chat response")
	}
	if !strings.Contains(err.Error(), "HTML") {
		t.Fatalf("expected clear HTML error, got: %s", err.Error())
	}
}

func TestChat_JSONResponseOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m1","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"total_tokens":5}}`))
	}))
	defer srv.Close()

	c := New("custom", srv.URL, "test-key", 5*time.Second)
	res, err := c.Chat(context.Background(), "m1", []ChatMessage{{Role: "user", Content: "hi"}}, nil, GenOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if res.Content != "hello" {
		t.Fatalf("unexpected content: %s", res.Content)
	}
}
