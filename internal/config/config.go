package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	APIToken       string
	Timeout        time.Duration
	UserAgent      string
	VerifyTLS      bool
	MaxPageSize    int
	APIBaseURL     string
	ServerName     string
	ServerVersion  string
	Protocol       string
	Transport      string
	HTTPAddr       string
	HTTPPath       string
	HTTPAuthToken  string
	AllowedOrigins []string
}

const (
	defaultTimeoutSeconds = 20
	defaultUserAgent      = "readeck-mcp/0.1"
	defaultMaxPageSize    = 100
	defaultTransport      = "stdio"
	defaultHTTPAddr       = "127.0.0.1:8080"
	defaultHTTPPath       = "/mcp"
)

func Load() (Config, error) {
	baseRaw := strings.TrimSpace(os.Getenv("READECK_BASE_URL"))
	if baseRaw == "" {
		return Config{}, errors.New("READECK_BASE_URL is required")
	}

	baseURL, err := url.Parse(baseRaw)
	if err != nil {
		return Config{}, fmt.Errorf("parse READECK_BASE_URL: %w", err)
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return Config{}, errors.New("READECK_BASE_URL must include scheme and host")
	}
	if err := validateScheme(baseURL); err != nil {
		return Config{}, err
	}

	token := strings.TrimSpace(os.Getenv("READECK_API_TOKEN"))
	if token == "" {
		return Config{}, errors.New("READECK_API_TOKEN is required")
	}

	timeoutSeconds, err := readIntEnv("READECK_TIMEOUT_SECONDS", defaultTimeoutSeconds)
	if err != nil {
		return Config{}, err
	}
	if timeoutSeconds <= 0 {
		return Config{}, errors.New("READECK_TIMEOUT_SECONDS must be > 0")
	}

	maxPageSize, err := readIntEnv("READECK_MAX_PAGE_SIZE", defaultMaxPageSize)
	if err != nil {
		return Config{}, err
	}
	if maxPageSize <= 0 {
		return Config{}, errors.New("READECK_MAX_PAGE_SIZE must be > 0")
	}

	verifyTLS, err := readBoolEnv("READECK_VERIFY_TLS", true)
	if err != nil {
		return Config{}, err
	}

	userAgent := strings.TrimSpace(os.Getenv("READECK_USER_AGENT"))
	if userAgent == "" {
		userAgent = defaultUserAgent
	}

	transport := strings.ToLower(strings.TrimSpace(os.Getenv("MCP_TRANSPORT")))
	if transport == "" {
		transport = defaultTransport
	}
	switch transport {
	case "stdio", "http", "streamable-http":
	default:
		return Config{}, errors.New("MCP_TRANSPORT must be one of: stdio, http, streamable-http")
	}

	httpAddr := strings.TrimSpace(os.Getenv("MCP_HTTP_ADDR"))
	if httpAddr == "" {
		httpAddr = defaultHTTPAddr
	}

	httpPath := strings.TrimSpace(os.Getenv("MCP_HTTP_PATH"))
	if httpPath == "" {
		httpPath = defaultHTTPPath
	}
	if !strings.HasPrefix(httpPath, "/") {
		httpPath = "/" + httpPath
	}

	httpAuthToken := strings.TrimSpace(os.Getenv("MCP_HTTP_AUTH_TOKEN"))
	allowedOrigins := parseCSV(os.Getenv("MCP_ALLOWED_ORIGINS"))

	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	apiBase := strings.TrimRight(baseURL.String(), "/") + "/api"

	cfg := Config{
		APIToken:       token,
		Timeout:        time.Duration(timeoutSeconds) * time.Second,
		UserAgent:      userAgent,
		VerifyTLS:      verifyTLS,
		MaxPageSize:    maxPageSize,
		APIBaseURL:     apiBase,
		ServerName:     "readeck-mcp",
		ServerVersion:  "0.1.0",
		Protocol:       "2025-06-18",
		Transport:      transport,
		HTTPAddr:       httpAddr,
		HTTPPath:       httpPath,
		HTTPAuthToken:  httpAuthToken,
		AllowedOrigins: allowedOrigins,
	}
	return cfg, nil
}

func NewHTTPClient(cfg Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: !cfg.VerifyTLS}
	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: transport,
	}
}

func readIntEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return v, nil
}

func readBoolEnv(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true/false", key)
	}
	return v, nil
}

func validateScheme(u *url.URL) error {
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme != "http" {
		return errors.New("READECK_BASE_URL must use https (or http for localhost)")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	return errors.New("READECK_BASE_URL must use https unless pointing to localhost")
}

func parseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}
