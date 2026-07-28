package main

// 本文件实现 samctl 命令和带超时的 SamKV HTTP 客户端，支持结构化日志、健康检查与指标。
// 响应体最多读取 64 MiB；非 2xx 响应转换为可用 errors.As 检查的 APIError。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAddress        = "localhost"
	defaultPort           = 9999
	defaultRequestTimeout = 10 * time.Second
	maxResponseBytes      = 64 << 20
)

var (
	ErrArgsNotEnough = errors.New("cli: required arguments are missing")
	ErrInvalidMode   = errors.New("cli: invalid mode")
)

type APIError struct {
	StatusCode int
	Status     string
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("request failed: %s", e.Status)
	}
	return fmt.Sprintf("request failed: %s: %s", e.Status, e.Message)
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type LogWrite struct {
	Timestamp *time.Time        `json:"timestamp,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Message   string            `json:"message"`
	Sequence  uint64            `json:"sequence,omitempty"`
}

type LogEntry struct {
	Timestamp time.Time         `json:"timestamp"`
	Labels    map[string]string `json:"labels"`
	Message   string            `json:"message"`
	Sequence  uint64            `json:"sequence"`
}

type LogQueryResult struct {
	Matcher   string     `json:"matcher"`
	Start     time.Time  `json:"start"`
	End       time.Time  `json:"end"`
	Entries   []LogEntry `json:"entries"`
	Truncated bool       `json:"truncated"`
}

// NewClient 创建 HTTP 客户端；address 传主机名或裸 IPv4/IPv6，不要包含协议和端口。
// port 必须为 1..65535，timeout 必须大于 0；当前客户端固定使用明文 HTTP。
func NewClient(address string, port int, timeout time.Duration) (*Client, error) {
	if address == "" {
		return nil, errors.New("cli: address is required")
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("cli: invalid port %d", port)
	}
	if timeout <= 0 {
		return nil, errors.New("cli: timeout must be greater than zero")
	}

	endpoint := url.URL{Scheme: "http", Host: net.JoinHostPort(address, fmt.Sprint(port))}
	return &Client{
		baseURL: endpoint.String(),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (c *Client) Health(ctx context.Context) (string, error) {
	body, err := c.request(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return "", err
	}
	var response struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("decode health response: %w", err)
	}
	return response.Status, nil
}

func (c *Client) WriteLog(ctx context.Context, entry LogWrite) (uint64, error) {
	body, err := json.Marshal(entry)
	if err != nil {
		return 0, err
	}
	data, err := c.request(ctx, http.MethodPost, c.baseURL+"/logs", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	var response struct {
		Sequence uint64 `json:"sequence"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return 0, fmt.Errorf("decode log write response: %w", err)
	}
	return response.Sequence, nil
}

func (c *Client) WriteLogs(ctx context.Context, entries []LogWrite) ([]uint64, error) {
	body, err := json.Marshal(struct {
		Entries []LogWrite `json:"entries"`
	}{Entries: entries})
	if err != nil {
		return nil, err
	}
	data, err := c.request(ctx, http.MethodPost, c.baseURL+"/logs/batch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var response struct {
		Sequences []uint64 `json:"sequences"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode log batch response: %w", err)
	}
	return response.Sequences, nil
}

// QueryLogs 把完整 QueryFormat 作为 query 参数发送；limit<=0 使用服务端默认值，超过服务端上限会得到 APIError。
func (c *Client) QueryLogs(ctx context.Context, query string, limit int) (LogQueryResult, error) {
	if query == "" {
		return LogQueryResult{}, fmt.Errorf("%w: query is required", ErrArgsNotEnough)
	}
	endpoint, err := url.Parse(c.baseURL + "/logs/query")
	if err != nil {
		return LogQueryResult{}, err
	}
	values := endpoint.Query()
	values.Set("query", query)
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	endpoint.RawQuery = values.Encode()

	body, err := c.request(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return LogQueryResult{}, err
	}
	var response LogQueryResult
	if err := json.Unmarshal(body, &response); err != nil {
		return LogQueryResult{}, fmt.Errorf("decode log query response: %w", err)
	}
	return response, nil
}

func (c *Client) Metrics(ctx context.Context) (string, error) {
	body, err := c.request(ctx, http.MethodGet, c.baseURL+"/metrics", nil)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *Client) request(ctx context.Context, method, endpoint string, body io.Reader) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request SamKV: %w", err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("cli: response exceeds 64 MiB")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, newAPIError(response, data)
	}
	return data, nil
}

func newAPIError(response *http.Response, body []byte) error {
	var payload struct {
		Error string `json:"error"`
	}
	message := ""
	if err := json.Unmarshal(body, &payload); err == nil {
		message = payload.Error
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	return &APIError{
		StatusCode: response.StatusCode,
		Status:     response.Status,
		Message:    message,
	}
}

type cliConfig struct {
	mode       string
	address    string
	port       int
	timeout    time.Duration
	message    string
	messageSet bool
	labels     labelValues
	timestamp  string
	sequence   uint64
	query      string
	limit      int
	batchFile  string
}

type optionalString struct {
	value string
	set   bool
}

func (s *optionalString) String() string {
	return s.value
}

func (s *optionalString) Set(value string) error {
	s.value = value
	s.set = true
	return nil
}

type labelValues map[string]string

func (labels *labelValues) String() string {
	if labels == nil || *labels == nil {
		return ""
	}
	pairs := make([]string, 0, len(*labels))
	for name, value := range *labels {
		pairs = append(pairs, name+"="+value)
	}
	return strings.Join(pairs, ",")
}

func (labels *labelValues) Set(value string) error {
	name, labelValue, ok := strings.Cut(value, "=")
	if !ok || name == "" {
		return errors.New("label must use name=value")
	}
	if *labels == nil {
		*labels = make(map[string]string)
	}
	(*labels)[name] = labelValue
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	config, err := parseCLIConfig(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if config.mode == "help" {
		writeCLIUsage(stdout, nil)
		return nil
	}

	client, err := NewClient(config.address, config.port, config.timeout)
	if err != nil {
		return err
	}
	ctx := context.Background()

	switch config.mode {
	case "health":
		status, err := client.Health(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, status)
		return err
	case "log":
		entry, err := config.logEntry()
		if err != nil {
			return err
		}
		sequence, err := client.WriteLog(ctx, entry)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, sequence)
		return err
	case "log-batch":
		entries, err := readLogBatch(config.batchFile)
		if err != nil {
			return err
		}
		sequences, err := client.WriteLogs(ctx, entries)
		if err != nil {
			return err
		}
		return writeJSON(stdout, struct {
			Sequences []uint64 `json:"sequences"`
		}{Sequences: sequences})
	case "query":
		result, err := client.QueryLogs(ctx, config.query, config.limit)
		if err != nil {
			return err
		}
		return writeJSON(stdout, result)
	case "metrics":
		metrics, err := client.Metrics(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(stdout, metrics)
		return err
	default:
		return fmt.Errorf("%w: %q", ErrInvalidMode, config.mode)
	}
}

func parseCLIConfig(args []string, output io.Writer) (cliConfig, error) {
	config := cliConfig{
		address: defaultAddress,
		port:    defaultPort,
		timeout: defaultRequestTimeout,
	}

	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		config.mode = strings.ToLower(args[0])
		args = args[1:]
	}

	flags := flag.NewFlagSet("samkv-cli", flag.ContinueOnError)
	flags.SetOutput(output)
	var message optionalString
	flags.StringVar(&config.mode, "m", config.mode, "mode: health, log, log-batch, query, metrics, help")
	flags.Var(&message, "message", "log message; empty string is allowed")
	flags.Var(&config.labels, "label", "log label as name=value; repeatable")
	flags.StringVar(&config.timestamp, "timestamp", "", "log timestamp in RFC3339 or RFC3339Nano")
	flags.Uint64Var(&config.sequence, "sequence", 0, "log sequence; 0 lets the server assign one")
	flags.StringVar(&config.query, "query", "", "QueryFormat expression for log query")
	flags.IntVar(&config.limit, "limit", 0, "log query limit; 0 uses server default")
	flags.StringVar(&config.batchFile, "file", "", "JSON file for log-batch")
	flags.StringVar(&config.address, "a", config.address, "server address")
	flags.IntVar(&config.port, "p", config.port, "server port")
	flags.DurationVar(&config.timeout, "timeout", config.timeout, "request timeout")
	flags.Usage = func() {
		writeCLIUsage(output, flags)
	}
	if err := flags.Parse(args); err != nil {
		return cliConfig{}, err
	}

	config.message = message.value
	config.messageSet = message.set
	config.mode = normalizeMode(config.mode)
	remaining := flags.Args()
	switch config.mode {
	case "log":
		if !config.messageSet && len(remaining) > 0 {
			config.message = remaining[0]
			config.messageSet = true
			remaining = remaining[1:]
		}
	case "query":
		if config.query == "" && len(remaining) > 0 {
			config.query = remaining[0]
			remaining = remaining[1:]
		}
	}
	if len(remaining) > 0 {
		return cliConfig{}, fmt.Errorf("cli: unexpected arguments: %s", strings.Join(remaining, " "))
	}
	if config.mode == "" {
		config.mode = "help"
	}

	switch config.mode {
	case "help":
	case "health":
	case "log":
		if !config.messageSet {
			return cliConfig{}, fmt.Errorf("%w: message is required for log", ErrArgsNotEnough)
		}
	case "log-batch":
		if config.batchFile == "" {
			return cliConfig{}, fmt.Errorf("%w: file is required for log-batch", ErrArgsNotEnough)
		}
	case "query":
		if config.query == "" {
			return cliConfig{}, fmt.Errorf("%w: query is required for query", ErrArgsNotEnough)
		}
	case "metrics":
	default:
		return cliConfig{}, fmt.Errorf("%w: %q", ErrInvalidMode, config.mode)
	}
	return config, nil
}

func normalizeMode(mode string) string {
	switch strings.ToLower(mode) {
	case "logs":
		return "log"
	default:
		return strings.ToLower(mode)
	}
}

func writeCLIUsage(output io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(output, "Usage / 用法:")
	fmt.Fprintln(output, "  samctl help")
	fmt.Fprintln(output, "      显示帮助信息")
	fmt.Fprintln(output, "  samctl health [-a address] [-p port]")
	fmt.Fprintln(output, "      检查服务健康状态")
	fmt.Fprintln(output, "  samctl log [-label name=value] [-timestamp time] [-sequence n] -message <message>")
	fmt.Fprintln(output, "      写入单条结构化日志，-label 可重复")
	fmt.Fprintln(output, "  samctl log-batch -file entries.json")
	fmt.Fprintln(output, "      从 JSON 文件批量写入结构化日志")
	fmt.Fprintln(output, "  samctl query [-limit n] <query>")
	fmt.Fprintln(output, "      使用 QueryFormat 查询结构化日志")
	fmt.Fprintln(output, "  samctl metrics [-a address] [-p port]")
	fmt.Fprintln(output, "      输出 Prometheus 指标")
	fmt.Fprintln(output, "  samctl -m <mode>")
	fmt.Fprintln(output, "      兼容旧的 -m 调用方式")
	if flags != nil {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Flags / 参数:")
		flags.PrintDefaults()
	}
}
func (config cliConfig) logEntry() (LogWrite, error) {
	entry := LogWrite{
		Labels:   map[string]string(config.labels),
		Message:  config.message,
		Sequence: config.sequence,
	}
	if config.timestamp != "" {
		timestamp, err := time.Parse(time.RFC3339Nano, config.timestamp)
		if err != nil {
			return LogWrite{}, fmt.Errorf("parse timestamp: %w", err)
		}
		entry.Timestamp = &timestamp
	}
	return entry, nil
}

func readLogBatch(path string) ([]LogWrite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read log batch: %w", err)
	}
	var wrapped struct {
		Entries []LogWrite `json:"entries"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Entries != nil {
		return wrapped.Entries, nil
	}
	var entries []LogWrite
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode log batch: %w", err)
	}
	return entries, nil
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
