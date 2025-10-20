// Package processor 提供WAT文件流式处理功能
package processor

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/slyrz/warc"
	"go.uber.org/zap"

	"github.com/yanyun/wp_crawl/internal/config"
	"github.com/yanyun/wp_crawl/internal/detector"
)

// WATProcessor 处理WAT文件的核心组件
type WATProcessor struct {
	config    *config.Config
	detector  *detector.Detector
	client    *retryablehttp.Client
	logger    *zap.Logger

	// 统计信息
	stats     *ProcessorStats
	statsMu   sync.RWMutex
}

// ProcessorStats 处理器统计信息
type ProcessorStats struct {
	TotalFiles      int64
	ProcessedFiles  int64
	TotalRecords    int64
	ProcessedRecords int64
	TotalHits       int64
	TotalErrors     int64
	BytesProcessed  int64
	StartTime       time.Time
	LastUpdateTime  time.Time
}

// NewWATProcessor 创建新的WAT处理器
func NewWATProcessor(cfg *config.Config, det *detector.Detector, logger *zap.Logger) *WATProcessor {
	// 创建带重试的HTTP客户端
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = cfg.Network.RetryAttempts
	retryClient.RetryWaitMin = cfg.Network.RetryDelay
	retryClient.RetryWaitMax = cfg.Network.RetryDelay * 10
	retryClient.HTTPClient.Timeout = cfg.Network.Timeout
	retryClient.Logger = nil // 使用自定义logger

	return &WATProcessor{
		config:   cfg,
		detector: det,
		client:   retryClient,
		logger:   logger,
		stats: &ProcessorStats{
			StartTime: time.Now(),
		},
	}
}

// ProcessWATFile 处理单个WAT文件
func (p *WATProcessor) ProcessWATFile(ctx context.Context, watURL string) (<-chan *detector.Hit, error) {
	hitChan := make(chan *detector.Hit, 100)

	go func() {
		defer close(hitChan)

		// 更新统计
		p.incrementFileCount()
		defer p.incrementProcessedFileCount()

		// 流式下载和处理
		if err := p.streamProcessWAT(ctx, watURL, hitChan); err != nil {
			p.logger.Error("Failed to process WAT file",
				zap.String("url", watURL),
				zap.Error(err))
			p.incrementErrorCount()
		}
	}()

	return hitChan, nil
}

// streamProcessWAT 流式处理WAT文件
func (p *WATProcessor) streamProcessWAT(ctx context.Context, watURL string, hitChan chan<- *detector.Hit) error {
	// 创建HTTP请求
	req, err := retryablehttp.NewRequestWithContext(ctx, "GET", watURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", p.config.Network.UserAgent)

	// 发送请求
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("download WAT: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	// 根据内容类型选择读取器
	reader, err := p.createReader(resp.Body, watURL)
	if err != nil {
		return fmt.Errorf("create reader: %w", err)
	}

	// 使用WARC库解析
	warcReader, err := warc.NewReader(reader)
	if err != nil {
		return fmt.Errorf("create WARC reader: %w", err)
	}

	// 处理每条记录
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		record, err := warcReader.ReadRecord()
		if err == io.EOF {
			break
		}
		if err != nil {
			p.logger.Warn("Failed to read WARC record",
				zap.String("url", watURL),
				zap.Error(err))
			p.incrementErrorCount()
			continue
		}

		// 更新记录统计
		p.incrementRecordCount()

		// 只处理metadata类型的记录
		if record.Header.Get("WARC-Type") != "metadata" {
			continue
		}

		// 读取记录内容
		content, err := io.ReadAll(record.Content)
		if err != nil {
			p.logger.Warn("Failed to read record content",
				zap.Error(err))
			p.incrementErrorCount()
			continue
		}

		// 更新处理字节数
		p.addBytesProcessed(int64(len(content)))

		// 检测WordPress API
		hit, err := p.detector.DetectInWATRecord(content)
		if err != nil {
			p.logger.Debug("Failed to detect in record",
				zap.Error(err))
			continue
		}

		if hit != nil {
			// 补充WAT文件信息
			hit.WATLocation = watURL

			select {
			case hitChan <- hit:
				p.incrementHitCount()
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		p.incrementProcessedRecordCount()
	}

	return nil
}

// createReader 根据文件类型创建适当的读取器
func (p *WATProcessor) createReader(body io.Reader, url string) (io.Reader, error) {
	// 检查是否是gzip文件
	if strings.HasSuffix(url, ".gz") {
		return gzip.NewReader(body)
	}
	return body, nil
}

// ProcessWATList 处理WAT文件列表
func (p *WATProcessor) ProcessWATList(ctx context.Context, watURLs []string) (<-chan *detector.Hit, error) {
	hitChan := make(chan *detector.Hit, 1000)

	// 创建工作池
	workerCount := p.config.Concurrency.Workers
	workChan := make(chan string, p.config.Concurrency.DownloadQueue)

	// 启动工作者
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			p.worker(ctx, workerID, workChan, hitChan)
		}(i)
	}

	// 发送任务
	go func() {
		defer close(workChan)
		for _, url := range watURLs {
			select {
			case workChan <- url:
			case <-ctx.Done():
				return
			}
		}
	}()

	// 等待完成并关闭输出通道
	go func() {
		wg.Wait()
		close(hitChan)
	}()

	return hitChan, nil
}

// worker 工作者函数
func (p *WATProcessor) worker(ctx context.Context, id int, workChan <-chan string, hitChan chan<- *detector.Hit) {
	p.logger.Info("Worker started", zap.Int("worker_id", id))

	for watURL := range workChan {
		select {
		case <-ctx.Done():
			p.logger.Info("Worker stopped by context", zap.Int("worker_id", id))
			return
		default:
		}

		p.logger.Debug("Processing WAT file",
			zap.Int("worker_id", id),
			zap.String("url", watURL))

		// 处理单个WAT文件
		hits, err := p.ProcessWATFile(ctx, watURL)
		if err != nil {
			p.logger.Error("Failed to process WAT file",
				zap.Int("worker_id", id),
				zap.String("url", watURL),
				zap.Error(err))
			continue
		}

		// 收集结果
		for hit := range hits {
			select {
			case hitChan <- hit:
			case <-ctx.Done():
				return
			}
		}
	}

	p.logger.Info("Worker finished", zap.Int("worker_id", id))
}

// GetStats 获取统计信息
func (p *WATProcessor) GetStats() ProcessorStats {
	p.statsMu.RLock()
	defer p.statsMu.RUnlock()
	return *p.stats
}

// 统计方法
func (p *WATProcessor) incrementFileCount() {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.stats.TotalFiles++
	p.stats.LastUpdateTime = time.Now()
}

func (p *WATProcessor) incrementProcessedFileCount() {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.stats.ProcessedFiles++
	p.stats.LastUpdateTime = time.Now()
}

func (p *WATProcessor) incrementRecordCount() {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.stats.TotalRecords++
	p.stats.LastUpdateTime = time.Now()
}

func (p *WATProcessor) incrementProcessedRecordCount() {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.stats.ProcessedRecords++
	p.stats.LastUpdateTime = time.Now()
}

func (p *WATProcessor) incrementHitCount() {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.stats.TotalHits++
	p.stats.LastUpdateTime = time.Now()
}

func (p *WATProcessor) incrementErrorCount() {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.stats.TotalErrors++
	p.stats.LastUpdateTime = time.Now()
}

func (p *WATProcessor) addBytesProcessed(bytes int64) {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.stats.BytesProcessed += bytes
	p.stats.LastUpdateTime = time.Now()
}

// StreamProcessor 提供更高级的流处理接口
type StreamProcessor struct {
	processor *WATProcessor
	buffer    *bufio.Reader
	logger    *zap.Logger
}

// NewStreamProcessor 创建流处理器
func NewStreamProcessor(processor *WATProcessor, logger *zap.Logger) *StreamProcessor {
	return &StreamProcessor{
		processor: processor,
		logger:    logger,
	}
}

// ProcessStream 处理输入流
func (s *StreamProcessor) ProcessStream(ctx context.Context, reader io.Reader) (<-chan *detector.Hit, error) {
	s.buffer = bufio.NewReaderSize(reader, 1024*1024) // 1MB缓冲
	hitChan := make(chan *detector.Hit, 100)

	go func() {
		defer close(hitChan)

		// 逐行读取处理
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line, err := s.buffer.ReadBytes('\n')
			if err == io.EOF {
				break
			}
			if err != nil {
				s.logger.Error("Failed to read stream", zap.Error(err))
				continue
			}

			// 检测记录
			hit, err := s.processor.detector.DetectInWATRecord(line)
			if err != nil {
				s.logger.Debug("Failed to detect in line", zap.Error(err))
				continue
			}

			if hit != nil {
				select {
				case hitChan <- hit:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return hitChan, nil
}