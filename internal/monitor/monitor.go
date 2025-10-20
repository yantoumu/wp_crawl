// Package monitor 提供监控和指标收集功能
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Monitor 监控器接口
type Monitor interface {
	RecordMetric(name string, value float64, labels map[string]string)
	IncrementCounter(name string, labels map[string]string)
	ObserveHistogram(name string, value float64, labels map[string]string)
	SetGauge(name string, value float64, labels map[string]string)
	Start() error
	Stop() error
}

// Metrics 指标数据
type Metrics struct {
	// 系统指标
	MemoryUsage      uint64    `json:"memory_usage_bytes"`
	GoroutineCount   int       `json:"goroutine_count"`
	CPUPercent       float64   `json:"cpu_percent"`

	// 扫描指标
	TotalWATFiles    int64     `json:"total_wat_files"`
	ProcessedWATFiles int64    `json:"processed_wat_files"`
	TotalRecords     int64     `json:"total_records"`
	ProcessedRecords int64     `json:"processed_records"`
	TotalHits        int64     `json:"total_hits"`
	ErrorCount       int64     `json:"error_count"`

	// 性能指标
	ProcessingRate   float64   `json:"processing_rate"`
	HitRate          float64   `json:"hit_rate"`
	BytesPerSecond   float64   `json:"bytes_per_second"`

	// 时间指标
	Uptime           time.Duration `json:"uptime"`
	LastUpdateTime   time.Time     `json:"last_update_time"`
}

// MetricsCollector 指标收集器
type MetricsCollector struct {
	metrics   *Metrics
	mu        sync.RWMutex
	startTime time.Time
	logger    *zap.Logger

	// HTTP服务器
	server    *http.Server
	port      int
}

// NewMetricsCollector 创建指标收集器
func NewMetricsCollector(port int, logger *zap.Logger) *MetricsCollector {
	return &MetricsCollector{
		metrics: &Metrics{
			LastUpdateTime: time.Now(),
		},
		startTime: time.Now(),
		logger:    logger,
		port:      port,
	}
}

// Start 启动指标服务器
func (mc *MetricsCollector) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", mc.metricsHandler)
	mux.HandleFunc("/health", mc.healthHandler)
	mux.HandleFunc("/stats", mc.statsHandler)

	mc.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", mc.port),
		Handler: mux,
	}

	// 启动系统指标收集
	go mc.collectSystemMetrics()

	// 启动HTTP服务器
	go func() {
		if err := mc.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			mc.logger.Error("Metrics server error", zap.Error(err))
		}
	}()

	mc.logger.Info("Metrics server started", zap.Int("port", mc.port))
	return nil
}

// Stop 停止指标服务器
func (mc *MetricsCollector) Stop() error {
	if mc.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return mc.server.Shutdown(ctx)
	}
	return nil
}

// RecordMetric 记录指标
func (mc *MetricsCollector) RecordMetric(name string, value float64, labels map[string]string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	switch name {
	case "wat_files_total":
		mc.metrics.TotalWATFiles = int64(value)
	case "wat_files_processed":
		mc.metrics.ProcessedWATFiles = int64(value)
	case "records_total":
		mc.metrics.TotalRecords = int64(value)
	case "records_processed":
		mc.metrics.ProcessedRecords = int64(value)
	case "hits_total":
		mc.metrics.TotalHits = int64(value)
	case "errors_total":
		mc.metrics.ErrorCount = int64(value)
	case "processing_rate":
		mc.metrics.ProcessingRate = value
	case "hit_rate":
		mc.metrics.HitRate = value
	case "bytes_per_second":
		mc.metrics.BytesPerSecond = value
	}

	mc.metrics.LastUpdateTime = time.Now()
}

// IncrementCounter 增加计数器
func (mc *MetricsCollector) IncrementCounter(name string, labels map[string]string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	switch name {
	case "hits":
		mc.metrics.TotalHits++
	case "errors":
		mc.metrics.ErrorCount++
	case "wat_files":
		mc.metrics.ProcessedWATFiles++
	case "records":
		mc.metrics.ProcessedRecords++
	}

	mc.metrics.LastUpdateTime = time.Now()
}

// ObserveHistogram 观察直方图
func (mc *MetricsCollector) ObserveHistogram(name string, value float64, labels map[string]string) {
	// 实现直方图观察逻辑
}

// SetGauge 设置仪表值
func (mc *MetricsCollector) SetGauge(name string, value float64, labels map[string]string) {
	mc.RecordMetric(name, value, labels)
}

// collectSystemMetrics 收集系统指标
func (mc *MetricsCollector) collectSystemMetrics() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		mc.mu.Lock()
		mc.metrics.MemoryUsage = m.Alloc
		mc.metrics.GoroutineCount = runtime.NumGoroutine()
		mc.metrics.Uptime = time.Since(mc.startTime)
		mc.metrics.LastUpdateTime = time.Now()
		mc.mu.Unlock()
	}
}

// metricsHandler 处理指标请求
func (mc *MetricsCollector) metricsHandler(w http.ResponseWriter, r *http.Request) {
	mc.mu.RLock()
	metrics := *mc.metrics
	mc.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

// healthHandler 处理健康检查请求
func (mc *MetricsCollector) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// statsHandler 处理统计请求
func (mc *MetricsCollector) statsHandler(w http.ResponseWriter, r *http.Request) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	stats := map[string]interface{}{
		"uptime":           mc.metrics.Uptime.String(),
		"total_wat_files":  mc.metrics.TotalWATFiles,
		"processed_files":  mc.metrics.ProcessedWATFiles,
		"total_hits":       mc.metrics.TotalHits,
		"error_count":      mc.metrics.ErrorCount,
		"processing_rate":  mc.metrics.ProcessingRate,
		"memory_usage_mb":  float64(mc.metrics.MemoryUsage) / (1024 * 1024),
		"goroutines":       mc.metrics.GoroutineCount,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// Logger 创建配置化的日志器
func NewLogger(level string, logFile string) (*zap.Logger, error) {
	// 解析日志级别
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log level: %w", err)
	}

	// 创建日志配置
	config := zap.Config{
		Level:            zap.NewAtomicLevelAt(zapLevel),
		Development:      zapLevel == zapcore.DebugLevel,
		Encoding:         "json",
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	// 添加文件输出
	if logFile != "" {
		config.OutputPaths = append(config.OutputPaths, logFile)
	}

	// 自定义编码器配置
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	config.EncoderConfig.StacktraceKey = "stacktrace"
	config.EncoderConfig.MessageKey = "message"
	config.EncoderConfig.LevelKey = "level"

	// 构建日志器
	logger, err := config.Build()
	if err != nil {
		return nil, fmt.Errorf("build logger: %w", err)
	}

	return logger, nil
}

// RateLimiter 速率限制器
type RateLimiter struct {
	rate      int           // 每秒允许的请求数
	burst     int           // 突发容量
	tokens    chan struct{} // 令牌桶
	ticker    *time.Ticker
	stopCh    chan struct{}
	mu        sync.Mutex
}

// NewRateLimiter 创建速率限制器
func NewRateLimiter(rate, burst int) *RateLimiter {
	rl := &RateLimiter{
		rate:   rate,
		burst:  burst,
		tokens: make(chan struct{}, burst),
		stopCh: make(chan struct{}),
	}

	// 初始化令牌桶
	for i := 0; i < burst; i++ {
		rl.tokens <- struct{}{}
	}

	// 启动令牌生成器
	rl.ticker = time.NewTicker(time.Second / time.Duration(rate))
	go rl.refill()

	return rl
}

// refill 补充令牌
func (rl *RateLimiter) refill() {
	for {
		select {
		case <-rl.ticker.C:
			select {
			case rl.tokens <- struct{}{}:
			default:
				// 令牌桶已满
			}
		case <-rl.stopCh:
			rl.ticker.Stop()
			return
		}
	}
}

// Wait 等待获取令牌
func (rl *RateLimiter) Wait(ctx context.Context) error {
	select {
	case <-rl.tokens:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop 停止速率限制器
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

// CircuitBreaker 熔断器
type CircuitBreaker struct {
	maxFailures  int
	resetTimeout time.Duration
	failures     int
	lastFailTime time.Time
	state        string // "closed", "open", "half-open"
	mu           sync.RWMutex
}

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		state:        "closed",
	}
}

// Call 执行调用
func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// 检查熔断器状态
	switch cb.state {
	case "open":
		// 检查是否可以进入半开状态
		if time.Since(cb.lastFailTime) > cb.resetTimeout {
			cb.state = "half-open"
			cb.failures = 0
		} else {
			return fmt.Errorf("circuit breaker is open")
		}
	}

	// 执行函数
	err := fn()

	if err != nil {
		cb.failures++
		cb.lastFailTime = time.Now()

		if cb.failures >= cb.maxFailures {
			cb.state = "open"
			return fmt.Errorf("circuit breaker opened: %w", err)
		}
	} else {
		// 成功，重置失败计数
		if cb.state == "half-open" {
			cb.state = "closed"
		}
		cb.failures = 0
	}

	return err
}

// GetState 获取熔断器状态
func (cb *CircuitBreaker) GetState() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}