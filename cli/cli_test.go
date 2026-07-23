package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClientPutGetDeleteAndHealth(t *testing.T) {
	server := newTestKVServer(t)
	client := newTestClient(t, server.URL)
	ctx := context.Background()

	if err := client.Put(ctx, "services/api?env=prod#blue", "ready"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	value, err := client.Get(ctx, "services/api?env=prod#blue")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "ready" {
		t.Fatalf("Get()=%q, want ready", value)
	}
	if err := client.Delete(ctx, "services/api?env=prod#blue"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	status, err := client.Health(ctx)
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if status != "ok" {
		t.Fatalf("Health()=%q, want ok", status)
	}
}

func TestClientAllowsEmptyValue(t *testing.T) {
	server := newTestKVServer(t)
	client := newTestClient(t, server.URL)

	if err := client.Put(context.Background(), "empty", ""); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	value, err := client.Get(context.Background(), "empty")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "" {
		t.Fatalf("Get()=%q, want empty", value)
	}
}

func TestClientReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"key not found"}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.Get(context.Background(), "missing")
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("error=%v, want APIError", err)
	}
	if apiError.StatusCode != http.StatusNotFound || apiError.Message != "key not found" {
		t.Fatalf("APIError=%#v", apiError)
	}
}

func TestRunSupportsSubcommandsAndFlags(t *testing.T) {
	server := newTestKVServer(t)
	address, port := testServerAddress(t, server.URL)
	connectionFlags := []string{"-a", address, "-p", strconv.Itoa(port)}

	var stdout strings.Builder
	putArgs := append([]string{"put"}, connectionFlags...)
	putArgs = append(putArgs, "cli/key", "value")
	if err := run(putArgs, &stdout, io.Discard); err != nil {
		t.Fatalf("run(put) error = %v", err)
	}
	if stdout.String() != "ok\n" {
		t.Fatalf("put output=%q", stdout.String())
	}

	stdout.Reset()
	getArgs := append([]string{"get"}, connectionFlags...)
	getArgs = append(getArgs, "cli/key")
	if err := run(getArgs, &stdout, io.Discard); err != nil {
		t.Fatalf("run(get) error = %v", err)
	}
	if stdout.String() != "value\n" {
		t.Fatalf("get output=%q", stdout.String())
	}

	stdout.Reset()
	healthArgs := append([]string{"health"}, connectionFlags...)
	if err := run(healthArgs, &stdout, io.Discard); err != nil {
		t.Fatalf("run(health) error = %v", err)
	}
	if stdout.String() != "ok\n" {
		t.Fatalf("health output=%q", stdout.String())
	}
}

func TestParseCLIConfigRequiresArguments(t *testing.T) {
	tests := [][]string{
		nil,
		{"get"},
		{"put", "key"},
		{"health", "unexpected"},
		{"unknown"},
	}
	for _, args := range tests {
		if _, err := parseCLIConfig(args, io.Discard); err == nil {
			t.Fatalf("parseCLIConfig(%q) succeeded", args)
		}
	}
}

func TestParseCLIConfigAcceptsExplicitEmptyValue(t *testing.T) {
	config, err := parseCLIConfig([]string{"put", "key", ""}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLIConfig() error = %v", err)
	}
	if !config.valueSet || config.value != "" {
		t.Fatalf("valueSet=%v value=%q", config.valueSet, config.value)
	}
}

func newTestClient(t *testing.T, rawURL string) *Client {
	t.Helper()
	address, port := testServerAddress(t, rawURL)
	client, err := NewClient(address, port, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func testServerAddress(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	host, rawPort, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func newTestKVServer(t *testing.T) *httptest.Server {
	t.Helper()
	values := make(map[string]string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"ok"}`)
			return
		}
		if !strings.HasPrefix(request.URL.Path, "/kv/") {
			http.NotFound(w, request)
			return
		}
		key := strings.TrimPrefix(request.URL.Path, "/kv/")
		switch request.Method {
		case http.MethodPut:
			var body struct {
				Value string `json:"value"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			values[key] = body.Value
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			value, ok := values[key]
			if !ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":"key not found"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"key": key, "value": value})
		case http.MethodDelete:
			delete(values, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, fmt.Sprintf("unsupported method %s", request.Method), http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
