// Package scanner 提供主扫描器功能
package scanner

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schollz/progressbar/v3"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanyun/wp_crawl/internal/config"
	"github.com/yanyun/wp_crawl/internal/detector"
	"github.com/yanyun/wp_crawl/internal/processor"
	"github.com/yanyun/wp_crawl/internal/storage"
)

// Scanner 主扫描器
type Scanner struct {
	config    *config.Config
	detector  *detector.Detector
	processor *processor.WATProcessor
	storage   storage.Storage
	logger    *zap.Logger

	// 状态管理
	state     *ScannerState
	stateMu   sync.RWMutex

	// 进度条
	progress  *progressbar.ProgressBar

	// 控制
	ctx       context.Context
	cancel    context.CancelFunc
}

// ScannerState 扫描器状态
type ScannerState struct {
	CrawlID         string    `json:"crawl_id"`
	CurrentSegment  string    `json:"current_segment"`
	CurrentWAT      int       `json:"current_wat"`
	TotalWATs       int       `json:"total_wats"`
	ProcessedWATs   int       `json:"processed_wats"`
	TotalHits       int64     `json:"total_hits"`
	StartTime       time.Time `json:"start_time"`
	LastUpdateTime  time.Time `json:"last_update_time"`
	CompletedSegments []string `json:"completed_segments"`
}

// NewScanner 创建新的扫描器
func NewScanner(cfg *config.Config, logger *zap.Logger) (*Scanner, error) {
	// 创建检测器
	det := detector.NewDetector(cfg.Detection.Patterns, cfg.Detection.PreFilter)

	// 创建处理器
	proc := processor.NewWATProcessor(cfg, det, logger)

	// 创建存储
	stor, err := storage.NewNDJSONStorage(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("create storage: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	scanner := &Scanner{
		config:    cfg,
		detector:  det,
		processor: proc,
		storage:   stor,
		logger:    logger,
		ctx:       ctx,
		cancel:    cancel,
		state: &ScannerState{
			CrawlID:   cfg.Crawl.Name,
			StartTime: time.Now(),
		},
	}

	// 加载状态（如果启用断点续扫）
	if cfg.Resume.Enable {
		if err := scanner.loadState(); err != nil {
			logger.Warn("Failed to load state", zap.Error(err))
		}
	}

	return scanner, nil
}

// Start 开始扫描
func (s *Scanner) Start() error {
	s.logger.Info("Starting scanner",
		zap.String("crawl", s.config.Crawl.Name),
		zap.Int("workers", s.config.Concurrency.Workers))

	// 获取WAT文件列表
	watURLs, err := s.getWATList()
	if err != nil {
		return fmt.Errorf("get WAT list: %w", err)
	}

	s.logger.Info("WAT files to process",
		zap.Int("total", len(watURLs)))

	// 更新状态
	s.updateState(func(state *ScannerState) {
		state.TotalWATs = len(watURLs)
	})

	// 创建进度条
	if s.config.Monitor.ProgressBar {
		s.progress = progressbar.NewOptions(len(watURLs),
			progressbar.OptionEnableColorCodes(true),
			progressbar.OptionShowBytes(false),
			progressbar.OptionSetWidth(50),
			progressbar.OptionSetDescription("Processing WAT files"),
			progressbar.OptionSetTheme(progressbar.Theme{
				Saucer:        "[green]=[reset]",
				SaucerHead:    "[green]>[reset]",
				SaucerPadding: " ",
				BarStart:      "[",
				BarEnd:        "]",
			}))
	}

	// 创建异步写入器
	asyncWriter := storage.NewAsyncWriter(s.storage, 1000, s.logger)

	// 处理WAT文件
	hitChan, err := s.processor.ProcessWATList(s.ctx, watURLs)
	if err != nil {
		return fmt.Errorf("process WAT list: %w", err)
	}

	// 启动状态保存协程
	if s.config.Resume.Enable {
		go s.periodicStateSave()
	}

	// 处理结果
	var totalHits int64
	for hit := range hitChan {
		if err := asyncWriter.Write(hit); err != nil {
			s.logger.Error("Failed to write hit", zap.Error(err))
			continue
		}

		atomic.AddInt64(&totalHits, 1)

		// 更新进度
		if s.progress != nil && atomic.LoadInt64(&totalHits)%100 == 0 {
			processedWATs := s.getProcessedWATCount()
			s.progress.Set(processedWATs)
		}
	}

	// 关闭写入器
	if err := asyncWriter.Close(); err != nil {
		s.logger.Error("Failed to close writer", zap.Error(err))
	}

	// 完成进度条
	if s.progress != nil {
		s.progress.Finish()
	}

	// 保存最终状态
	if s.config.Resume.Enable {
		if err := s.saveState(); err != nil {
			s.logger.Error("Failed to save final state", zap.Error(err))
		}
	}

	// 输出统计信息
	s.printStats(totalHits)

	return nil
}

// Stop 停止扫描
func (s *Scanner) Stop() {
	s.logger.Info("Stopping scanner")
	s.cancel()

	// 保存状态
	if s.config.Resume.Enable {
		if err := s.saveState(); err != nil {
			s.logger.Error("Failed to save state on stop", zap.Error(err))
		}
	}

	// 关闭存储
	if err := s.storage.Close(); err != nil {
		s.logger.Error("Failed to close storage", zap.Error(err))
	}
}

// getWATList 获取WAT文件列表
func (s *Scanner) getWATList() ([]string, error) {
	// 获取爬虫路径文件
	pathsURL := fmt.Sprintf("https://data.commoncrawl.org/crawl-data/%s/wat.paths.gz",
		s.config.Crawl.Name)

	resp, err := http.Get(pathsURL)
	if err != nil {
		return nil, fmt.Errorf("fetch paths: %w", err)
	}
	defer resp.Body.Close()

	// 创建 gzip 解压读取器
	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// 读取并解析路径
	content, err := io.ReadAll(gzReader)
	if err != nil {
		return nil, fmt.Errorf("read paths: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	var watURLs []string

	// 过滤和转换为完整URL
	baseURL := "https://data.commoncrawl.org/"
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// 检查是否从指定位置开始
		if i < s.config.Crawl.StartFrom {
			continue
		}

		// 检查是否在指定段中
		if len(s.config.Crawl.Segments) > 0 {
			inSegment := false
			for _, seg := range s.config.Crawl.Segments {
				if strings.Contains(line, seg) {
					inSegment = true
					break
				}
			}
			if !inSegment {
				continue
			}
		}

		// 检查是否已完成
		if s.isSegmentCompleted(line) {
			continue
		}

		watURLs = append(watURLs, baseURL+line)
	}

	return watURLs, nil
}

// isSegmentCompleted 检查段是否已完成
func (s *Scanner) isSegmentCompleted(path string) bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()

	for _, completed := range s.state.CompletedSegments {
		if path == completed {
			return true
		}
	}
	return false
}

// loadState 加载状态
func (s *Scanner) loadState() error {
	stateFile := s.config.Resume.StateFile
	if stateFile == "" {
		stateFile = fmt.Sprintf("scanner_state_%s.json", s.config.Crawl.Name)
	}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在是正常的
		}
		return fmt.Errorf("read state file: %w", err)
	}

	var state ScannerState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("unmarshal state: %w", err)
	}

	s.stateMu.Lock()
	s.state = &state
	s.stateMu.Unlock()

	s.logger.Info("State loaded",
		zap.String("crawl", state.CrawlID),
		zap.Int("processed", state.ProcessedWATs),
		zap.Int64("hits", state.TotalHits))

	return nil
}

// saveState 保存状态
func (s *Scanner) saveState() error {
	s.stateMu.RLock()
	state := *s.state
	s.stateMu.RUnlock()

	state.LastUpdateTime = time.Now()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	stateFile := s.config.Resume.StateFile
	if stateFile == "" {
		stateFile = fmt.Sprintf("scanner_state_%s.json", s.config.Crawl.Name)
	}

	// 写入临时文件后重命名（原子操作）
	tmpFile := stateFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}

	if err := os.Rename(tmpFile, stateFile); err != nil {
		return fmt.Errorf("rename state file: %w", err)
	}

	return nil
}

// periodicStateSave 定期保存状态
func (s *Scanner) periodicStateSave() {
	ticker := time.NewTicker(s.config.Resume.SaveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := s.saveState(); err != nil {
				s.logger.Error("Failed to save state", zap.Error(err))
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// updateState 更新状态
func (s *Scanner) updateState(fn func(*ScannerState)) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	fn(s.state)
	s.state.LastUpdateTime = time.Now()
}

// getProcessedWATCount 获取已处理的WAT文件数
func (s *Scanner) getProcessedWATCount() int {
	stats := s.processor.GetStats()
	return int(stats.ProcessedFiles)
}

// printStats 打印统计信息
func (s *Scanner) printStats(totalHits int64) {
	procStats := s.processor.GetStats()
	storStats := s.storage.GetStats()

	duration := time.Since(s.state.StartTime)

	fmt.Println("\n========== Scan Statistics ==========")
	fmt.Printf("Crawl ID:         %s\n", s.config.Crawl.Name)
	fmt.Printf("Duration:         %s\n", duration.Round(time.Second))
	fmt.Printf("WAT Files:        %d / %d\n", procStats.ProcessedFiles, procStats.TotalFiles)
	fmt.Printf("Records:          %d / %d\n", procStats.ProcessedRecords, procStats.TotalRecords)
	fmt.Printf("Total Hits:       %d\n", totalHits)
	fmt.Printf("Errors:           %d\n", procStats.TotalErrors)
	fmt.Printf("Bytes Processed:  %.2f GB\n", float64(procStats.BytesProcessed)/(1024*1024*1024))
	fmt.Printf("Output File:      %s\n", storStats.CurrentFile)
	fmt.Printf("Output Size:      %.2f MB\n", float64(storStats.TotalBytes)/(1024*1024))
	fmt.Printf("Processing Rate:  %.2f records/sec\n", float64(procStats.ProcessedRecords)/duration.Seconds())
	fmt.Println("=====================================")
}

// ParallelScanner 并行扫描器
type ParallelScanner struct {
	config    *config.Config
	logger    *zap.Logger
	scanners  []*Scanner
}

// NewParallelScanner 创建并行扫描器
func NewParallelScanner(cfg *config.Config, logger *zap.Logger) *ParallelScanner {
	return &ParallelScanner{
		config: cfg,
		logger: logger,
	}
}

// Start 启动并行扫描
func (ps *ParallelScanner) Start(segments []string) error {
	g, _ := errgroup.WithContext(context.Background())

	for _, segment := range segments {
		segment := segment // 捕获循环变量

		g.Go(func() error {
			// 创建段特定的配置
			segCfg := *ps.config
			segCfg.Crawl.Segments = []string{segment}

			// 创建扫描器
			scanner, err := NewScanner(&segCfg, ps.logger.With(zap.String("segment", segment)))
			if err != nil {
				return fmt.Errorf("create scanner for segment %s: %w", segment, err)
			}

			ps.scanners = append(ps.scanners, scanner)

			// 启动扫描
			return scanner.Start()
		})
	}

	return g.Wait()
}

// Stop 停止所有扫描器
func (ps *ParallelScanner) Stop() {
	for _, scanner := range ps.scanners {
		scanner.Stop()
	}
}