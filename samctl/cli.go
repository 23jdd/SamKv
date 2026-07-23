package main

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
	"strings"
	"time"
)

const (
	defaultAddress        = "127.0.0.1"
	defaultPort           = 9999
	defaultRequestTimeout = 10 * time.Second
	maxResponseBytes      = 64 << 20
)

var (
	// ErrArgsNotEnough 表示当前命令缺少 key、value 等必需参数。
	ErrArgsNotEnough = errors.New("cli: required arguments are missing")
	// ErrInvalidMode 表示请求模式不是 get、put、del 或 health。
	ErrInvalidMode = errors.New("cli: invalid mode")
)

// APIError 描述 SamKV HTTP 服务返回的非成功响应。
type APIError struct {
	StatusCode int
	Status     string
	Message    string
}

// Error 返回包含 HTTP 状态和服务端消息的错误文本。
func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("request failed: %s", e.Status)
	}
	return fmt.Sprintf("request failed: %s: %s", e.Status, e.Message)
}

// Client 是 SamKV HTTP KV API 的轻量客户端。
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient 创建连接指定 SamKV HTTP 服务的客户端。
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

// Get 读取 key，成功时返回对应 value。
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("%w: key is required", ErrArgsNotEnough)
	}
	body, err := c.request(ctx, http.MethodGet, c.keyURL(key), nil)
	if err != nil {
		return "", err
	}
	var response struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("decode GET response: %w", err)
	}
	return response.Value, nil
}

// Put 写入 key/value；value 可以是空字符串。
func (c *Client) Put(ctx context.Context, key, value string) error {
	if key == "" {
		return fmt.Errorf("%w: key is required", ErrArgsNotEnough)
	}
	body, err := json.Marshal(struct {
		Value string `json:"value"`
	}{Value: value})
	if err != nil {
		return err
	}
	_, err = c.request(ctx, http.MethodPut, c.keyURL(key), bytes.NewReader(body))
	return err
}

// Delete 删除 key。服务端删除接口是幂等的，key 不存在时也视为成功。
func (c *Client) Delete(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("%w: key is required", ErrArgsNotEnough)
	}
	_, err := c.request(ctx, http.MethodDelete, c.keyURL(key), nil)
	return err
}

// Health 检查 SamKV HTTP 服务及后台维护状态。
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

func (c *Client) keyURL(key string) string {
	// 对整个 key 做路径转义；服务端的通配符路由会在接收时恢复其中的斜杠。
	return c.baseURL + "/kv/" + url.PathEscape(key)
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
	mode     string
	key      string
	value    string
	valueSet bool
	address  string
	port     int
	timeout  time.Duration
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

	client, err := NewClient(config.address, config.port, config.timeout)
	if err != nil {
		return err
	}
	ctx := context.Background()

	switch config.mode {
	case "get":
		value, err := client.Get(ctx, config.key)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, value)
		return err
	case "put":
		if err := client.Put(ctx, config.key, config.value); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "ok")
		return err
	case "del":
		if err := client.Delete(ctx, config.key); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "ok")
		return err
	case "health":
		status, err := client.Health(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, status)
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

	// 支持 `samkv-cli get ...`，同时保留原有的 `-m get` 调用方式。
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		config.mode = strings.ToLower(args[0])
		args = args[1:]
	}

	flags := flag.NewFlagSet("samkv-cli", flag.ContinueOnError)
	flags.SetOutput(output)
	var value optionalString
	flags.StringVar(&config.mode, "m", config.mode, "mode: get, put, del, health")
	flags.StringVar(&config.key, "k", "", "key")
	flags.Var(&value, "v", "value; empty string is allowed")
	flags.StringVar(&config.address, "a", config.address, "server address")
	flags.IntVar(&config.port, "p", config.port, "server port")
	flags.DurationVar(&config.timeout, "timeout", config.timeout, "request timeout")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage:")
		fmt.Fprintln(output, "  samkv-cli get [-a address] [-p port] <key>")
		fmt.Fprintln(output, "  samkv-cli put [-a address] [-p port] <key> <value>")
		fmt.Fprintln(output, "  samkv-cli del [-a address] [-p port] <key>")
		fmt.Fprintln(output, "  samkv-cli health [-a address] [-p port]")
		fmt.Fprintln(output, "  samkv-cli -m <mode> -k <key> [-v value]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return cliConfig{}, err
	}

	remaining := flags.Args()
	if config.key == "" && len(remaining) > 0 {
		config.key = remaining[0]
		remaining = remaining[1:]
	}
	if !value.set && len(remaining) > 0 {
		value.value = remaining[0]
		value.set = true
		remaining = remaining[1:]
	}
	if len(remaining) > 0 {
		return cliConfig{}, fmt.Errorf("cli: unexpected arguments: %s", strings.Join(remaining, " "))
	}
	config.value = value.value
	config.valueSet = value.set
	config.mode = strings.ToLower(config.mode)
	if config.mode == "delete" {
		config.mode = "del"
	}

	switch config.mode {
	case "get", "del":
		if config.key == "" {
			return cliConfig{}, fmt.Errorf("%w: key is required for %s", ErrArgsNotEnough, config.mode)
		}
	case "put":
		if config.key == "" {
			return cliConfig{}, fmt.Errorf("%w: key is required for put", ErrArgsNotEnough)
		}
		if !config.valueSet {
			return cliConfig{}, fmt.Errorf("%w: value is required for put", ErrArgsNotEnough)
		}
	case "health":
		if config.key != "" || config.valueSet {
			return cliConfig{}, errors.New("cli: health does not accept key or value")
		}
	case "":
		return cliConfig{}, fmt.Errorf("%w: mode is required", ErrArgsNotEnough)
	default:
		return cliConfig{}, fmt.Errorf("%w: %q", ErrInvalidMode, config.mode)
	}
	return config, nil
}
