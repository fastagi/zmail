package config

import (
	"fmt"
	"net"
	"strings"
	"time"
)

type AppConfig struct {
	Name  string
	Env   string
	Debug bool
	URL   string
}

type ServerConfig struct {
	Host              string
	Port              int
	Mode              string
	ReadTimeout       int
	ReadHeaderTimeout int
	WriteTimeout      int
	IdleTimeout       int
	MaxHeaderBytes    int
	TrustedProxies    []string
	// DiagnosticsAddr serves Go runtime profiles (pprof) when set. It must be a loopback address;
	// reach it with `kubectl port-forward` or an SSH tunnel, never through the public listener.
	DiagnosticsAddr string
}

// MiddlewareConfig holds middleware configuration.
type MiddlewareConfig struct {
	RequestTimeout          int                           // Request timeout in seconds, default 180 (3 min)
	BodyLimit               int64                         // Max body size in bytes, default 10MB
	RateLimit               RateLimitConfig               // Production default request rate guardrail
	AuthenticationRateLimit AuthenticationRateLimitConfig // Public authentication abuse guardrails
	RateLimitStore          RateLimitStoreConfig          // Backend shared by every rate-limit rule
}

// Rate-limit store backends and outage policies.
const (
	RateLimitStoreMemory   = "memory"
	RateLimitStorePostgres = "postgres"

	RateLimitStoreFailureLocal  = "local"
	RateLimitStoreFailureOpen   = "open"
	RateLimitStoreFailureClosed = "closed"

	DefaultRateLimitStoreTimeout = 50 * time.Millisecond
	MaxRateLimitStoreTimeout     = time.Second
)

// RateLimitStoreConfig selects where fixed-window counters live. The default memory store enforces
// each budget per replica; postgres shares one budget across replicas at the cost of one database
// round trip per decision. Failure decides what a decision does when that round trip fails or
// exceeds Timeout: local (enforce a process-local budget), open (allow), or closed (deny).
type RateLimitStoreConfig struct {
	Driver  string
	Timeout time.Duration
	Failure string
}

// DefaultRateLimitMaxBuckets bounds one process-local limiter store by default.
const DefaultRateLimitMaxBuckets = 10_000

// RateLimitConfig holds default HTTP rate limit configuration.
type RateLimitConfig struct {
	Enabled    bool
	Max        int
	Window     time.Duration
	MaxBuckets int
	SkipPaths  []string
}

// RateLimitRuleConfig defines one fixed-window quota.
type RateLimitRuleConfig struct {
	Max    int
	Window time.Duration
}

// AuthenticationEndpointRateLimitConfig applies independent quotas to the
// request source and the normalized, hashed subject targeted by the request.
type AuthenticationEndpointRateLimitConfig struct {
	PerIP      RateLimitRuleConfig
	PerSubject RateLimitRuleConfig
}

// AuthenticationRateLimitConfig controls public authentication endpoint
// guardrails. Per-subject rules may be left at zero when they do not add a
// meaningful boundary for an endpoint.
type AuthenticationRateLimitConfig struct {
	Enabled              bool
	MaxBucketsPerRule    int
	Login                AuthenticationEndpointRateLimitConfig
	Register             AuthenticationEndpointRateLimitConfig
	PasswordReset        AuthenticationEndpointRateLimitConfig
	PasswordResetConfirm AuthenticationEndpointRateLimitConfig
	RegisterEmailCode    AuthenticationEndpointRateLimitConfig
}

// MetricsConfig controls HTTP request instrumentation and the Prometheus endpoint.
type MetricsConfig struct {
	Enabled bool
}

type QueueConfig struct {
	Driver            string
	DefaultQueue      string
	BufferSize        int
	WorkerConcurrency int
	WorkerSleep       time.Duration
	WorkerTimeout     time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
}

type SchedulerConfig struct {
	Enabled bool
}

type LogConfig struct {
	Level       string
	File        string
	Stdout      bool
	FileEnabled bool
	JSON        bool
}

type SentryConfig struct {
	DSN string
}

type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	AllowCredentials bool
}

// TracingConfig holds OpenTelemetry tracing configuration.
type TracingConfig struct {
	Enabled    bool
	Endpoint   string  // OTLP endpoint (e.g., "localhost:4317")
	Insecure   bool    // Use insecure connection
	SampleRate float64 // Sampling rate (0.0 to 1.0)
}

func validateQueueConfig(queue QueueConfig, databaseEnabled bool) error {
	switch strings.ToLower(strings.TrimSpace(queue.Driver)) {
	case "sync", "memory":
	case "postgres":
		if !databaseEnabled {
			return fmt.Errorf("QUEUE_DRIVER=postgres requires DB_ENABLED=true")
		}
	default:
		return fmt.Errorf("QUEUE_DRIVER must be sync, memory, or postgres")
	}
	if strings.TrimSpace(queue.DefaultQueue) == "" || len(queue.DefaultQueue) > 128 {
		return fmt.Errorf("QUEUE_DEFAULT must contain 1 to 128 characters")
	}
	if queue.BufferSize < 1 || queue.WorkerConcurrency < 1 || queue.WorkerSleep <= 0 || queue.WorkerTimeout <= 0 {
		return fmt.Errorf("queue buffer, concurrency, sleep, and timeout values must be greater than 0")
	}
	if queue.LeaseDuration <= queue.WorkerTimeout {
		return fmt.Errorf("QUEUE_LEASE_DURATION must be greater than QUEUE_WORKER_TIMEOUT")
	}
	if queue.HeartbeatInterval <= 0 || queue.HeartbeatInterval >= queue.LeaseDuration {
		return fmt.Errorf("QUEUE_HEARTBEAT_INTERVAL must be greater than 0 and less than QUEUE_LEASE_DURATION")
	}
	return nil
}

func validateServerTransport(server ServerConfig, middleware MiddlewareConfig) error {
	if err := validateDiagnosticsAddr(server.DiagnosticsAddr); err != nil {
		return err
	}
	values := []struct {
		name  string
		value int
	}{
		{name: "SERVER_READ_TIMEOUT", value: server.ReadTimeout},
		{name: "SERVER_READ_HEADER_TIMEOUT", value: server.ReadHeaderTimeout},
		{name: "SERVER_WRITE_TIMEOUT", value: server.WriteTimeout},
		{name: "SERVER_IDLE_TIMEOUT", value: server.IdleTimeout},
		{name: "SERVER_MAX_HEADER_BYTES", value: server.MaxHeaderBytes},
		{name: "MIDDLEWARE_REQUEST_TIMEOUT", value: middleware.RequestTimeout},
	}
	for _, item := range values {
		if item.value < 0 {
			return fmt.Errorf("%s must not be negative", item.name)
		}
	}

	if server.ReadTimeout > 0 && server.ReadHeaderTimeout > server.ReadTimeout {
		return fmt.Errorf("SERVER_READ_HEADER_TIMEOUT must not exceed SERVER_READ_TIMEOUT")
	}

	requestTimeout := middleware.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = DefaultMiddlewareRequestTimeoutSeconds
	}
	if server.WriteTimeout > 0 && server.WriteTimeout <= requestTimeout {
		return fmt.Errorf(
			"SERVER_WRITE_TIMEOUT must exceed MIDDLEWARE_REQUEST_TIMEOUT (%d <= %d); set SERVER_WRITE_TIMEOUT=0 only when intentionally disabling the transport write deadline",
			server.WriteTimeout,
			requestTimeout,
		)
	}

	return nil
}

func validateTrustedProxies(proxies []string) error {
	for _, value := range proxies {
		proxy := strings.TrimSpace(value)
		if proxy == "" {
			return fmt.Errorf("SERVER_TRUSTED_PROXIES contains an empty value")
		}

		if strings.Contains(proxy, "/") {
			_, network, err := net.ParseCIDR(proxy)
			if err != nil {
				return fmt.Errorf("SERVER_TRUSTED_PROXIES contains invalid CIDR %q", proxy)
			}
			ones, _ := network.Mask.Size()
			if ones == 0 {
				return fmt.Errorf("SERVER_TRUSTED_PROXIES must not trust every address: %q", proxy)
			}
			continue
		}

		if net.ParseIP(proxy) == nil {
			return fmt.Errorf("SERVER_TRUSTED_PROXIES contains invalid IP %q", proxy)
		}
	}
	return nil
}

func validateRateLimitRule(prefix string, rule RateLimitRuleConfig, required bool) error {
	if rule.Max <= 0 {
		if !required && rule.Max == 0 {
			return nil
		}
		return fmt.Errorf("%s_MAX must be greater than 0 when authentication rate limit is enabled", prefix)
	}
	if rule.Window <= 0 {
		return fmt.Errorf("%s_WINDOW must be greater than 0 when authentication rate limit is enabled", prefix)
	}
	return nil
}

// validateDiagnosticsAddr keeps runtime profiles off every non-loopback interface: they expose
// memory contents, goroutine stacks, and command-line arguments.
func validateDiagnosticsAddr(addr string) error {
	if addr == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fmt.Errorf("SERVER_DIAGNOSTICS_ADDR must be host:port, for example 127.0.0.1:6060")
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("SERVER_DIAGNOSTICS_ADDR must listen on a loopback address such as 127.0.0.1")
	}
	return nil
}
