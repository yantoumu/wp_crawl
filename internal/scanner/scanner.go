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
	"github.com/yanyun/wp_crawl/internal/domain"
	"github.com/yanyun/wp_crawl/internal/exporter"
	"github.com/yanyun/wp_crawl/internal/monitor"
	"github.com/yanyun/wp_crawl/internal/processor"
	"github.com/yanyun/wp_crawl/internal/storage"
)

// Scanner 主扫描器
type Scanner struct {
	config        *config.Config
	detector      *detector.Detector
	processor     *processor.WATProcessor
	storage       storage.Storage
	domainStorage *domain.Storage  // 域名存储
	apiExporter   *exporter.APIExporter // API导出器
	logger        *zap.Logger

	// ⚡ 内存监控
	memMonitor    *monitor.MemoryMonitor

	// 状态管理
	state     *ScannerState
	stateMu   sync.RWMutex

	// 进度条
	progress  *progressbar.ProgressBar

	// 控制
	ctx       context.Context
	cancel    context.CancelFunc

	// 域名提取器（按 WAT 文件）
	currentExtractor *domain.Extractor
	currentWATURL    string
	extractorMu      sync.Mutex
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

	// 创建域名存储（输出目录为 domains）
	domainStor, err := domain.NewStorage("domains", logger)
	if err != nil {
		return nil, fmt.Errorf("create domain storage: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// ⚡ 创建内存监控器
	memMonitor := monitor.NewMemoryMonitor(cfg.Performance.MaxMemoryMB, logger)

	// ⚡ 创建API导出器（如果启用）
	var apiExp *exporter.APIExporter
	if cfg.Exporter.Enable {
		apiExp = exporter.NewAPIExporter(exporter.Config{
			APIURL:    cfg.Exporter.APIURL,
			APIKey:    cfg.Exporter.APIKey,
			BatchSize: cfg.Exporter.BatchSize,
			Timeout:   cfg.Exporter.Timeout,
		}, logger)
		logger.Info("API exporter enabled",
			zap.String("api_url", cfg.Exporter.APIURL),
			zap.Int("batch_size", cfg.Exporter.BatchSize))
	}

	scanner := &Scanner{
		config:        cfg,
		detector:      det,
		processor:     proc,
		storage:       stor,
		domainStorage: domainStor,
		apiExporter:   apiExp,
		logger:        logger,
		memMonitor:    memMonitor,
		ctx:           ctx,
		cancel:        cancel,
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

	// ⚡ 启动内存监控
	s.memMonitor.Start()
	defer s.memMonitor.Stop()

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
	if s.config.Logging.ProgressBar {
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

	// 启动定期保存主域名文件协程（每5分钟保存一次）
	go s.periodicMasterSave(5 * time.Minute)

	// 处理结果并提取域名
	var totalHits int64
	currentExtractor := domain.NewExtractor()
	var currentWATURL string

	// 失败计数器：防止日志炸弹
	var writeFailures int64
	const maxWriteFailures = 10

	for hit := range hitChan {
		// 写入 hit 到存储
		if err := asyncWriter.Write(hit); err != nil {
			atomic.AddInt64(&writeFailures, 1)
			failures := atomic.LoadInt64(&writeFailures)

			// 只记录前N次失败，防止疯狂写日志
			if failures <= maxWriteFailures {
				s.logger.Error("Failed to write hit",
					zap.Error(err),
					zap.Int64("failure_count", failures))
			} else if failures == maxWriteFailures+1 {
				s.logger.Error("Too many write failures, suppressing further logs",
					zap.Int64("total_failures", failures))
			}

			// 达到临界值时暂停等待，防止磁盘填满
			if failures > 1000 {
				s.logger.Warn("Critical: write failure threshold exceeded, pausing for 30s",
					zap.Int64("failures", failures))
				time.Sleep(30 * time.Second)
				// 重置计数器，继续尝试
				atomic.StoreInt64(&writeFailures, 0)
			}
			continue
		}
		// 写入成功，重置失败计数
		atomic.StoreInt64(&writeFailures, 0)

		atomic.AddInt64(&totalHits, 1)

		// 检测 WAT 文件是否切换（处理完一个文件）
		if currentWATURL != "" && hit.WATLocation != currentWATURL {
			// 保存上一个 WAT 文件的域名-语言对
			domainLangPairs := currentExtractor.GetDomainLanguagePairs()
			if len(domainLangPairs) > 0 {
				if err := s.domainStorage.SaveBatch(currentWATURL, domainLangPairs); err != nil {
					s.logger.Error("Failed to save domains for WAT file",
						zap.String("wat_url", currentWATURL),
						zap.Error(err))
				}
			}

			// 重置提取器
			currentExtractor = domain.NewExtractor()
		}

		// 更新当前 WAT URL
		currentWATURL = hit.WATLocation

		// 提取域名和语言（所有 hit 都有评论表单，直接提取）
		if err := currentExtractor.AddFromURLWithLanguage(hit.URL, hit.Language); err != nil {
			s.logger.Debug("Failed to extract domain",
				zap.String("url", hit.URL),
				zap.Error(err))
		}

		// ⚡ 导出到外部API（如果启用）
		if s.apiExporter != nil {
			if err := s.apiExporter.Add(hit.URL, hit.Language); err != nil {
				s.logger.Error("Failed to export to API",
					zap.String("url", hit.URL),
					zap.Error(err))
			}
		}

		// 更新进度（降低频率：100 → 1000，减少90%系统调用）
		if s.progress != nil && atomic.LoadInt64(&totalHits)%1000 == 0 {
			processedWATs := s.getProcessedWATCount()
			s.progress.Set(processedWATs)
		}
	}

	// 保存最后一个 WAT 文件的域名-语言对
	if currentWATURL != "" {
		domainLangPairs := currentExtractor.GetDomainLanguagePairs()
		if len(domainLangPairs) > 0 {
			if err := s.domainStorage.SaveBatch(currentWATURL, domainLangPairs); err != nil {
				s.logger.Error("Failed to save domains for last WAT file",
					zap.String("wat_url", currentWATURL),
					zap.Error(err))
			}
		}
	}

	// 保存最终的主域名文件（所有批次合并去重）
	if err := s.domainStorage.SaveMaster(); err != nil {
		s.logger.Error("Failed to save master domains file", zap.Error(err))
	} else {
		s.logger.Info("All domains saved",
			zap.Int("total_unique", s.domainStorage.GetGlobalCount()))
	}

	// ⚡ 刷新API导出器缓冲区
	if s.apiExporter != nil {
		if err := s.apiExporter.Flush(); err != nil {
			s.logger.Error("Failed to flush API exporter", zap.Error(err))
		} else {
			sent, fails := s.apiExporter.GetStats()
			s.logger.Info("API export completed",
				zap.Int64("total_sent", sent),
				zap.Int64("total_fails", fails))
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

	// 域名存储统计（主文件已在 Start() 结束时保存）
	if s.domainStorage != nil {
		s.logger.Info("Domain extraction completed",
			zap.Int("total_unique_domains", s.domainStorage.GetGlobalCount()))
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
	// ⚡ 性能优化: 使用内存状态检查，而非磁盘文件检查
	// 旧逻辑: 检查 domains/raw/ 目录下是否存在标记文件
	// 新逻辑: 使用 domain.Storage 的内存状态

	// 方法1: 检查状态文件中的记录
	s.stateMu.RLock()
	for _, completed := range s.state.CompletedSegments {
		if path == completed {
			s.stateMu.RUnlock()
			return true
		}
	}
	s.stateMu.RUnlock()

	// 方法2: 检查域名存储的内存状态（更高效）
	// 构建完整的 WAT URL 用于检查
	baseURL := "https://data.commoncrawl.org/"
	fullURL := baseURL + path

	if s.domainStorage.IsWATProcessed(fullURL) {
		s.logger.Debug("Skipping already processed WAT file",
			zap.String("path", path))
		return true
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

// periodicMasterSave 定期保存主域名文件（去重汇总）
func (s *Scanner) periodicMasterSave(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if s.domainStorage != nil {
				if err := s.domainStorage.SaveMaster(); err != nil {
					s.logger.Error("Failed to save master domains file periodically", zap.Error(err))
				} else {
					s.logger.Info("Master domains file saved periodically",
						zap.Int("total_unique_domains", s.domainStorage.GetGlobalCount()))
				}
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
	stats := s.processor.GetStatsSnapshot()
	return int(stats.ProcessedFiles)
}

// printStats 打印统计信息
func (s *Scanner) printStats(totalHits int64) {
	procStats := s.processor.GetStatsSnapshot()
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