// Package storage 提供结果存储功能
package storage

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/yanyun/wp_crawl/internal/config"
	"github.com/yanyun/wp_crawl/internal/detector"
)

// Storage 接口定义存储操作
type Storage interface {
	Write(hit *detector.Hit) error
	Flush() error
	Close() error
	GetStats() StorageStats
}

// StorageStats 存储统计信息
type StorageStats struct {
	TotalWrites    int64
	TotalBytes     int64
	LastWriteTime  time.Time
	LastFlushTime  time.Time
	CurrentFile    string
}

// NDJSONStorage NDJSON格式存储实现
type NDJSONStorage struct {
	config     *config.Config
	logger     *zap.Logger

	file       *os.File
	writer     io.Writer
	encoder    *json.Encoder

	buffer     []byte
	bufferSize int

	stats      *StorageStats
	statsMu    sync.RWMutex

	mu         sync.Mutex
	flushTimer *time.Timer
	closed     bool
}

// NewNDJSONStorage 创建NDJSON存储
func NewNDJSONStorage(cfg *config.Config, logger *zap.Logger) (*NDJSONStorage, error) {
	// 生成文件名
	filename := generateFilename(cfg)
	filePath := filepath.Join(cfg.Output.Directory, filename)

	// 创建目录
	if err := os.MkdirAll(cfg.Output.Directory, 0755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	// 打开文件
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}

	// 创建写入器
	var writer io.Writer = file
	if cfg.Output.Compress {
		gzWriter := gzip.NewWriter(file)
		writer = gzWriter
	}

	storage := &NDJSONStorage{
		config:     cfg,
		logger:     logger,
		file:       file,
		writer:     writer,
		encoder:    json.NewEncoder(writer),
		buffer:     make([]byte, 0, cfg.Output.BufferSize),
		bufferSize: cfg.Output.BufferSize,
		stats: &StorageStats{
			CurrentFile: filePath,
		},
	}

	// 启动定时刷新
	storage.startFlushTimer()

	logger.Info("Storage initialized",
		zap.String("file", filePath),
		zap.Bool("compressed", cfg.Output.Compress))

	return storage, nil
}

// Write 写入一条记录
func (s *NDJSONStorage) Write(hit *detector.Hit) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("storage is closed")
	}

	// 编码为JSON
	data, err := json.Marshal(hit)
	if err != nil {
		return fmt.Errorf("marshal hit: %w", err)
	}

	// 添加换行符
	data = append(data, '\n')

	// 写入缓冲区
	s.buffer = append(s.buffer, data...)

	// 更新统计
	s.updateWriteStats(int64(len(data)))

	// 检查是否需要刷新
	if len(s.buffer) >= s.bufferSize {
		return s.flushLocked()
	}

	return nil
}

// Flush 刷新缓冲区到磁盘
func (s *NDJSONStorage) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

// flushLocked 内部刷新方法（需要持有锁）
func (s *NDJSONStorage) flushLocked() error {
	if len(s.buffer) == 0 {
		return nil
	}

	// 写入数据
	_, err := s.writer.Write(s.buffer)
	if err != nil {
		return fmt.Errorf("write to file: %w", err)
	}

	// 如果是gzip，刷新压缩器
	if gzWriter, ok := s.writer.(*gzip.Writer); ok {
		if err := gzWriter.Flush(); err != nil {
			return fmt.Errorf("flush gzip: %w", err)
		}
	}

	// 同步到磁盘
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}

	// 清空缓冲区
	s.buffer = s.buffer[:0]

	// 更新统计
	s.updateFlushStats()

	s.logger.Debug("Buffer flushed",
		zap.String("file", s.stats.CurrentFile),
		zap.Int64("writes", s.stats.TotalWrites))

	return nil
}

// Close 关闭存储
func (s *NDJSONStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	// 停止刷新定时器
	if s.flushTimer != nil {
		s.flushTimer.Stop()
	}

	// 刷新剩余数据
	if err := s.flushLocked(); err != nil {
		s.logger.Error("Failed to flush on close", zap.Error(err))
	}

	// 关闭gzip写入器
	if gzWriter, ok := s.writer.(*gzip.Writer); ok {
		if err := gzWriter.Close(); err != nil {
			return fmt.Errorf("close gzip: %w", err)
		}
	}

	// 关闭文件
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}

	s.logger.Info("Storage closed",
		zap.String("file", s.stats.CurrentFile),
		zap.Int64("total_writes", s.stats.TotalWrites),
		zap.Int64("total_bytes", s.stats.TotalBytes))

	return nil
}

// GetStats 获取统计信息
func (s *NDJSONStorage) GetStats() StorageStats {
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()
	return *s.stats
}

// startFlushTimer 启动定时刷新
func (s *NDJSONStorage) startFlushTimer() {
	if s.config.Output.FlushInterval <= 0 {
		return
	}

	s.flushTimer = time.AfterFunc(s.config.Output.FlushInterval, func() {
		if err := s.Flush(); err != nil {
			s.logger.Error("Auto flush failed", zap.Error(err))
		}
		s.startFlushTimer() // 重新启动定时器
	})
}

// updateWriteStats 更新写入统计
func (s *NDJSONStorage) updateWriteStats(bytes int64) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.stats.TotalWrites++
	s.stats.TotalBytes += bytes
	s.stats.LastWriteTime = time.Now()
}

// updateFlushStats 更新刷新统计
func (s *NDJSONStorage) updateFlushStats() {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.stats.LastFlushTime = time.Now()
}

// generateFilename 生成输出文件名
func generateFilename(cfg *config.Config) string {
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s_%s_%s.ndjson",
		cfg.Output.FilePrefix,
		cfg.Crawl.Name,
		timestamp)

	if cfg.Output.Compress {
		filename += ".gz"
	}

	return filename
}

// BatchWriter 批量写入器
type BatchWriter struct {
	storage Storage
	batch   []*detector.Hit
	maxSize int
	mu      sync.Mutex
}

// NewBatchWriter 创建批量写入器
func NewBatchWriter(storage Storage, batchSize int) *BatchWriter {
	return &BatchWriter{
		storage: storage,
		batch:   make([]*detector.Hit, 0, batchSize),
		maxSize: batchSize,
	}
}

// Add 添加到批次
func (b *BatchWriter) Add(hit *detector.Hit) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.batch = append(b.batch, hit)

	if len(b.batch) >= b.maxSize {
		return b.flushBatch()
	}

	return nil
}

// Flush 刷新批次
func (b *BatchWriter) Flush() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.flushBatch()
}

// flushBatch 内部刷新批次
func (b *BatchWriter) flushBatch() error {
	if len(b.batch) == 0 {
		return nil
	}

	for _, hit := range b.batch {
		if err := b.storage.Write(hit); err != nil {
			return err
		}
	}

	b.batch = b.batch[:0]
	return b.storage.Flush()
}

// AsyncWriter 异步写入器
type AsyncWriter struct {
	storage   Storage
	writeChan chan *detector.Hit
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	logger    *zap.Logger
}

// NewAsyncWriter 创建异步写入器
func NewAsyncWriter(storage Storage, bufferSize int, logger *zap.Logger) *AsyncWriter {
	ctx, cancel := context.WithCancel(context.Background())

	aw := &AsyncWriter{
		storage:   storage,
		writeChan: make(chan *detector.Hit, bufferSize),
		ctx:       ctx,
		cancel:    cancel,
		logger:    logger,
	}

	aw.wg.Add(1)
	go aw.writeLoop()

	return aw
}

// Write 异步写入
func (a *AsyncWriter) Write(hit *detector.Hit) error {
	select {
	case a.writeChan <- hit:
		return nil
	case <-a.ctx.Done():
		return fmt.Errorf("writer stopped")
	}
}

// writeLoop 写入循环
func (a *AsyncWriter) writeLoop() {
	defer a.wg.Done()

	batch := NewBatchWriter(a.storage, 100)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case hit := <-a.writeChan:
			if err := batch.Add(hit); err != nil {
				a.logger.Error("Failed to write hit", zap.Error(err))
			}

		case <-ticker.C:
			if err := batch.Flush(); err != nil {
				a.logger.Error("Failed to flush batch", zap.Error(err))
			}

		case <-a.ctx.Done():
			// 处理剩余数据
			for hit := range a.writeChan {
				if err := batch.Add(hit); err != nil {
					a.logger.Error("Failed to write remaining hit", zap.Error(err))
				}
			}
			if err := batch.Flush(); err != nil {
				a.logger.Error("Failed to final flush", zap.Error(err))
			}
			return
		}
	}
}

// Close 关闭异步写入器
func (a *AsyncWriter) Close() error {
	a.cancel()
	close(a.writeChan)
	a.wg.Wait()
	return a.storage.Close()
}