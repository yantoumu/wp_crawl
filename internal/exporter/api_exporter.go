// Package exporter 提供域名批量导出到外部API的功能
package exporter

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// LinkItem 表示一个链接项
type LinkItem struct {
	URL      string `json:"url"`
	Language string `json:"language"`
}

// BatchRequest 批量导入请求
type BatchRequest struct {
	Links []LinkItem `json:"links"`
}

// APIExporter 外部API导出器
type APIExporter struct {
	apiURL     string
	apiKey     string
	batchSize  int
	client     *http.Client
	logger     *zap.Logger

	// 批量缓冲
	buffer     []LinkItem
	bufferMu   sync.Mutex

	// 统计
	totalSent  int64
	totalFails int64
	statsMu    sync.RWMutex
}

// Config API导出器配置
type Config struct {
	APIURL    string
	APIKey    string
	BatchSize int
	Timeout   time.Duration
}

// NewAPIExporter 创建新的API导出器
func NewAPIExporter(cfg Config, logger *zap.Logger) *APIExporter {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 500 // 默认500个一批
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}

	return &APIExporter{
		apiURL:    cfg.APIURL,
		apiKey:    cfg.APIKey,
		batchSize: cfg.BatchSize,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		logger: logger,
		buffer: make([]LinkItem, 0, cfg.BatchSize),
	}
}

// Add 添加一个链接到批量缓冲
func (e *APIExporter) Add(url, language string) error {
	e.bufferMu.Lock()
	defer e.bufferMu.Unlock()

	// 添加到缓冲区
	e.buffer = append(e.buffer, LinkItem{
		URL:      url,
		Language: language,
	})

	// 如果达到批量大小，立即发送
	if len(e.buffer) >= e.batchSize {
		return e.flushLocked()
	}

	return nil
}

// Flush 强制发送缓冲区中的所有数据
func (e *APIExporter) Flush() error {
	e.bufferMu.Lock()
	defer e.bufferMu.Unlock()

	return e.flushLocked()
}

// flushLocked 发送缓冲区数据（需要持有锁）
func (e *APIExporter) flushLocked() error {
	if len(e.buffer) == 0 {
		return nil
	}

	// 复制缓冲区（避免长时间持有锁）
	batch := make([]LinkItem, len(e.buffer))
	copy(batch, e.buffer)

	// 清空缓冲区
	e.buffer = e.buffer[:0]

	// 释放锁后发送（避免阻塞）
	e.bufferMu.Unlock()
	err := e.sendBatch(batch)
	e.bufferMu.Lock()

	return err
}

// sendBatch 发送一批数据到API
func (e *APIExporter) sendBatch(links []LinkItem) error {
	if len(links) == 0 {
		return nil
	}

	// 构造请求体
	reqBody := BatchRequest{
		Links: links,
	}

	// JSON编码
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		e.incrementFails()
		return fmt.Errorf("marshal request: %w", err)
	}

	// Gzip压缩
	var compressedBuf bytes.Buffer
	gzWriter := gzip.NewWriter(&compressedBuf)
	if _, err := gzWriter.Write(jsonData); err != nil {
		gzWriter.Close()
		e.incrementFails()
		return fmt.Errorf("gzip compress: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		e.incrementFails()
		return fmt.Errorf("close gzip writer: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequest("POST", e.apiURL, &compressedBuf)
	if err != nil {
		e.incrementFails()
		return fmt.Errorf("create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("X-API-Key", e.apiKey)

	// 发送请求
	startTime := time.Now()
	resp, err := e.client.Do(req)
	if err != nil {
		e.incrementFails()
		e.logger.Error("Failed to send batch",
			zap.Int("batch_size", len(links)),
			zap.Error(err))
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, _ := io.ReadAll(resp.Body)

	// 检查响应状态
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		e.incrementFails()
		e.logger.Error("API returned error",
			zap.Int("status_code", resp.StatusCode),
			zap.String("response", string(respBody)),
			zap.Int("batch_size", len(links)))
		return fmt.Errorf("API error: status=%d, body=%s", resp.StatusCode, string(respBody))
	}

	// 发送成功
	e.incrementSent(int64(len(links)))
	duration := time.Since(startTime)

	e.logger.Debug("Batch sent successfully",
		zap.Int("batch_size", len(links)),
		zap.Duration("duration", duration),
		zap.Int64("total_sent", e.getTotalSent()))

	return nil
}

// GetStats 获取统计信息
func (e *APIExporter) GetStats() (sent, fails int64) {
	e.statsMu.RLock()
	defer e.statsMu.RUnlock()
	return e.totalSent, e.totalFails
}

// incrementSent 增加发送计数
func (e *APIExporter) incrementSent(count int64) {
	e.statsMu.Lock()
	e.totalSent += count
	e.statsMu.Unlock()
}

// incrementFails 增加失败计数
func (e *APIExporter) incrementFails() {
	e.statsMu.Lock()
	e.totalFails++
	e.statsMu.Unlock()
}

// getTotalSent 获取总发送数
func (e *APIExporter) getTotalSent() int64 {
	e.statsMu.RLock()
	defer e.statsMu.RUnlock()
	return e.totalSent
}
