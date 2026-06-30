package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientSearchPostsToIndexSearchPath(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
	}))
	defer srv.Close()

	c := NewClient([]string{srv.URL}, "group_message")
	raw, err := c.Search(context.Background(), map[string]any{"size": 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/group_message/_search" {
		t.Fatalf("expected /group_message/_search, got %s", gotPath)
	}
	if !strings.Contains(gotBody, "\"size\":1") {
		t.Fatalf("expected body forwarded, got %s", gotBody)
	}
	if !strings.Contains(string(raw), "hits") {
		t.Fatalf("expected response returned, got %s", raw)
	}
}

func TestClientIndexUpsertsByID(t *testing.T) {
	var gotPath, gotMethod string
	var gotDoc map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotDoc)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"created"}`))
	}))
	defer srv.Close()

	c := NewClient([]string{srv.URL}, "group_message")
	err := c.Index(context.Background(), "m1", map[string]any{"message_id": "m1", "group_id": 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("expected PUT, got %s", gotMethod)
	}
	if gotPath != "/group_message/_doc/m1" {
		t.Fatalf("expected /group_message/_doc/m1, got %s", gotPath)
	}
	if gotDoc["message_id"] != "m1" {
		t.Fatalf("expected doc forwarded, got %#v", gotDoc)
	}
}

func TestClientSearchReturnsErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	c := NewClient([]string{srv.URL}, "group_message")
	if _, err := c.Search(context.Background(), map[string]any{}); err == nil {
		t.Fatal("expected error on 500 response")
	}
}
