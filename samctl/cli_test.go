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
	"os"
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

func TestClientLogBatchQueryAndMetrics(t *testing.T) {
	server := newTestKVServer(t)
	client := newTestClient(t, server.URL)
	ctx := context.Background()

	sequence, err := client.WriteLog(ctx, LogWrite{
		Labels:  map[string]string{"app": "api"},
		Message: "request started",
	})
	if err != nil {
		t.Fatalf("WriteLog() error = %v", err)
	}
	if sequence != 1 {
		t.Fatalf("WriteLog() sequence=%d, want 1", sequence)
	}

	sequences, err := client.WriteLogs(ctx, []LogWrite{
		{Labels: map[string]string{"app": "api"}, Message: "request failed"},
		{Labels: map[string]string{"app": "worker"}, Message: "job done"},
	})
	if err != nil {
		t.Fatalf("WriteLogs() error = %v", err)
	}
	if fmt.Sprint(sequences) != "[2 3]" {
		t.Fatalf("WriteLogs() sequences=%v, want [2 3]", sequences)
	}

	result, err := client.QueryLogs(ctx, `request{app=api}[1h]`, 2)
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	if result.Matcher != "request" || len(result.Entries) != 1 || result.Entries[0].Message != "request started" {
		t.Fatalf("QueryLogs()=%#v", result)
	}

	metrics, err := client.Metrics(ctx)
	if err != nil {
		t.Fatalf("Metrics() error = %v", err)
	}
	if !strings.Contains(metrics, "samkv_write_operations_total") {
		t.Fatalf("Metrics()=%q, want samkv metric", metrics)
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

func TestRunSupportsLogQueryBatchAndMetrics(t *testing.T) {
	server := newTestKVServer(t)
	address, port := testServerAddress(t, server.URL)
	connectionFlags := []string{"-a", address, "-p", strconv.Itoa(port)}

	var stdout strings.Builder
	logArgs := append([]string{"log"}, connectionFlags...)
	logArgs = append(logArgs, "-label", "app=api", "-message", "request started")
	if err := run(logArgs, &stdout, io.Discard); err != nil {
		t.Fatalf("run(log) error = %v", err)
	}
	if stdout.String() != "1\n" {
		t.Fatalf("log output=%q", stdout.String())
	}

	stdout.Reset()
	queryArgs := append([]string{"query"}, connectionFlags...)
	queryArgs = append(queryArgs, "-limit", "5", `request{app=api}[1h]`)
	if err := run(queryArgs, &stdout, io.Discard); err != nil {
		t.Fatalf("run(query) error = %v", err)
	}
	if !strings.Contains(stdout.String(), `"matcher": "request"`) {
		t.Fatalf("query output=%q", stdout.String())
	}

	batchPath := t.TempDir() + "/entries.json"
	if err := os.WriteFile(batchPath, []byte(`[
		{"labels":{"app":"api"},"message":"second"},
		{"labels":{"app":"api"},"message":"third"}
	]`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	batchArgs := append([]string{"log-batch"}, connectionFlags...)
	batchArgs = append(batchArgs, "-file", batchPath)
	if err := run(batchArgs, &stdout, io.Discard); err != nil {
		t.Fatalf("run(log-batch) error = %v", err)
	}
	if !strings.Contains(stdout.String(), `"sequences": [`) {
		t.Fatalf("log-batch output=%q", stdout.String())
	}

	stdout.Reset()
	metricsArgs := append([]string{"metrics"}, connectionFlags...)
	if err := run(metricsArgs, &stdout, io.Discard); err != nil {
		t.Fatalf("run(metrics) error = %v", err)
	}
	if !strings.Contains(stdout.String(), "samkv_write_operations_total") {
		t.Fatalf("metrics output=%q", stdout.String())
	}
}

func TestRunSupportsHelp(t *testing.T) {
	tests := [][]string{
		nil,
		{"help"},
		{"-m", "help"},
	}
	for _, args := range tests {
		var stdout strings.Builder
		if err := run(args, &stdout, io.Discard); err != nil {
			t.Fatalf("run(%v) error = %v", args, err)
		}
		output := stdout.String()
		if !strings.Contains(output, "Usage / 用法:") || !strings.Contains(output, "samctl log") || !strings.Contains(output, "samctl metrics") || !strings.Contains(output, "写入单条结构化日志") {
			t.Fatalf("help output=%q", output)
		}
	}
}
func TestParseCLIConfigRequiresArguments(t *testing.T) {
	tests := [][]string{
		{"get"},
		{"put", "key"},
		{"health", "unexpected"},
		{"log"},
		{"log-batch"},
		{"query"},
		{"metrics", "unexpected"},
		{"help", "unexpected"},
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

func TestParseCLIConfigAcceptsExplicitEmptyLogMessage(t *testing.T) {
	config, err := parseCLIConfig([]string{"log", "-message", ""}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLIConfig() error = %v", err)
	}
	if !config.messageSet || config.message != "" {
		t.Fatalf("messageSet=%v message=%q", config.messageSet, config.message)
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
	var sequence uint64
	logs := make([]LogWrite, 0)
	now := time.Date(2026, 7, 24, 10, 30, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/healthz":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"ok"}`)
			return
		case "/metrics":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "samkv_write_operations_total 3\n")
			return
		case "/logs":
			if request.Method != http.MethodPost {
				http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
				return
			}
			var entry LogWrite
			if err := json.NewDecoder(request.Body).Decode(&entry); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sequence++
			logs = append(logs, entry)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]uint64{"sequence": sequence})
			return
		case "/logs/batch":
			if request.Method != http.MethodPost {
				http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
				return
			}
			var body struct {
				Entries []LogWrite `json:"entries"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sequences := make([]uint64, 0, len(body.Entries))
			for _, entry := range body.Entries {
				sequence++
				sequences = append(sequences, sequence)
				logs = append(logs, entry)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string][]uint64{"sequences": sequences})
			return
		case "/logs/query":
			if request.Method != http.MethodGet {
				http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
				return
			}
			if request.URL.Query().Get("query") == "" {
				http.Error(w, "query is required", http.StatusBadRequest)
				return
			}
			if request.URL.Query().Get("limit") == "" {
				http.Error(w, "limit was not forwarded", http.StatusBadRequest)
				return
			}
			entries := make([]LogEntry, 0)
			for i, entry := range logs {
				if entry.Message == "request started" {
					entries = append(entries, LogEntry{
						Timestamp: now,
						Labels:    entry.Labels,
						Message:   entry.Message,
						Sequence:  uint64(i + 1),
					})
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(LogQueryResult{
				Matcher:   "request",
				Start:     now.Add(-time.Hour),
				End:       now,
				Entries:   entries,
				Truncated: false,
			})
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
