package domain

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Storage 域名存储管理器
type Storage struct {
	outputDir       string            // 输出目录
	rawDir          string            // 原始域名目录（已废弃，保留用于兼容性）
	masterFile      string            // 主域名文件（全局去重）
	stateFile       string            // 断点续传状态文件
	logger          *zap.Logger       // 日志记录器

	// ⚡ 性能优化: 使用分片 map 减少锁竞争
	globalDomains   *ShardedMap       // 全局域名->语言映射（分片存储）

	// 断点续传状态
	processedWATs   map[string]bool   // 内存记录已处理的 WAT 文件
	processedMu     sync.RWMutex      // 保护已处理列表

	// 内存管理
	lastSaveTime    time.Time         // 上次保存时间
	saveCounter     int               // 保存计数器（用于定期持久化）
}

// NewStorage 创建新的域名存储管理器
func NewStorage(outputDir string, logger *zap.Logger) (*Storage, error) {
	// 创建输出目录结构
	rawDir := filepath.Join(outputDir, "raw")
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create raw directory: %w", err)
	}

	masterFile := filepath.Join(outputDir, "domains.txt")
	stateFile := filepath.Join(outputDir, "processed_wats.state")

	s := &Storage{
		outputDir:     outputDir,
		rawDir:        rawDir,
		masterFile:    masterFile,
		stateFile:     stateFile,
		logger:        logger,
		globalDomains: NewShardedMap(32), // ⚡ 32 个分片，减少锁竞争
		processedWATs: make(map[string]bool),
		lastSaveTime:  time.Now(),
		saveCounter:   0,
	}

	// 加载已有的主域名文件（如果存在）
	if err := s.loadMasterFile(); err != nil {
		logger.Warn("Failed to load existing master file", zap.Error(err))
	}

	// ⚡ 加载断点续传状态
	if err := s.loadProcessedState(); err != nil {
		logger.Warn("Failed to load processed state", zap.Error(err))
	}

	return s, nil
}

// SaveBatch 保存一个批次的域名（处理完一个 WAT 文件）
// fileName: WAT 文件名（用于记录已处理状态）
// domainLangPairs: 该文件提取的域名-语言对列表（格式：domain,language）
func (s *Storage) SaveBatch(fileName string, domainLangPairs []string) error {
	// ⚡ 性能优化: 使用分片 map，无需全局锁

	// 更新全局域名集合（分片存储，自动分散锁竞争）
	for _, pair := range domainLangPairs {
		parts := strings.SplitN(pair, ",", 3)
		if len(parts) >= 2 {
			domain := parts[0]
			language := parts[1]

			// 检查是否需要更新
			if existingLang, exists := s.globalDomains.Get(domain); !exists || (language != "" && existingLang == "") {
				s.globalDomains.Set(domain, language)
			}
		}
	}

	// 记录已处理的 WAT 文件（用于断点续传）
	s.processedMu.Lock()
	s.processedWATs[fileName] = true
	s.saveCounter++
	needSave := s.saveCounter%100 == 0 // 每处理 100 个文件持久化一次
	s.processedMu.Unlock()

	// ⚡ 定期持久化断点续传状态（减少磁盘 I/O）
	if needSave {
		if err := s.saveProcessedState(); err != nil {
			s.logger.Warn("Failed to save processed state", zap.Error(err))
		}
	}

	return nil
}

// IsWATProcessed 检查 WAT 文件是否已处理（用于断点续传）
func (s *Storage) IsWATProcessed(fileName string) bool {
	s.processedMu.RLock()
	defer s.processedMu.RUnlock()
	return s.processedWATs[fileName]
}

// SaveMaster 保存主域名文件（全局去重后的所有域名）
func (s *Storage) SaveMaster() error {
	// ⚡ 使用分片 map 并发获取所有域名
	allDomains := s.globalDomains.GetAll()

	if len(allDomains) == 0 {
		s.logger.Warn("No domains to save to master file")
		return nil
	}

	// 构建域名-语言对列表
	domainLangPairs := make([]string, 0, len(allDomains))
	for domain, language := range allDomains {
		pair := domain + "," + language
		domainLangPairs = append(domainLangPairs, pair)
	}

	// 排序
	sort.Strings(domainLangPairs)

	// 写入主文件
	file, err := os.Create(s.masterFile)
	if err != nil {
		return fmt.Errorf("failed to create master file: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, pair := range domainLangPairs {
		if _, err := writer.WriteString(pair + "\n"); err != nil {
			return fmt.Errorf("failed to write domain-language pair to master: %w", err)
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush master file: %w", err)
	}

	s.logger.Info("Master domains file saved",
		zap.String("file", s.masterFile),
		zap.Int("total_unique", len(domainLangPairs)))

	// ⚡ 同时保存断点续传状态
	if err := s.saveProcessedState(); err != nil {
		s.logger.Warn("Failed to save processed state", zap.Error(err))
	}

	return nil
}

// loadMasterFile 加载已有的主域名文件
func (s *Storage) loadMasterFile() error {
	file, err := os.Open(s.masterFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在，不是错误
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			// 解析 domain,language 格式
			parts := strings.SplitN(line, ",", 2)
			if len(parts) == 2 {
				domain := parts[0]
				language := parts[1]
				s.globalDomains.Set(domain, language) // 使用分片map
			} else {
				// 兼容旧格式（只有域名）
				s.globalDomains.Set(line, "")
			}
			count++
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	s.logger.Info("Loaded existing master file",
		zap.String("file", s.masterFile),
		zap.Int("domain_pairs", count))

	return nil
}

// saveProcessedState 保存断点续传状态到磁盘
func (s *Storage) saveProcessedState() error {
	s.processedMu.RLock()
	processedList := make([]string, 0, len(s.processedWATs))
	for fileName := range s.processedWATs {
		processedList = append(processedList, fileName)
	}
	s.processedMu.RUnlock()

	// 序列化为 JSON
	data, err := json.MarshalIndent(processedList, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal processed state: %w", err)
	}

	// 原子写入（先写临时文件，再重命名）
	tmpFile := s.stateFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	if err := os.Rename(tmpFile, s.stateFile); err != nil {
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	s.logger.Debug("Processed state saved",
		zap.String("file", s.stateFile),
		zap.Int("count", len(processedList)))

	return nil
}

// loadProcessedState 从磁盘加载断点续传状态
func (s *Storage) loadProcessedState() error {
	data, err := os.ReadFile(s.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在，不是错误
		}
		return fmt.Errorf("failed to read state file: %w", err)
	}

	var processedList []string
	if err := json.Unmarshal(data, &processedList); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	s.processedMu.Lock()
	for _, fileName := range processedList {
		s.processedWATs[fileName] = true
	}
	s.processedMu.Unlock()

	s.logger.Info("Loaded processed state",
		zap.String("file", s.stateFile),
		zap.Int("count", len(processedList)))

	return nil
}

// GetGlobalCount 获取全局唯一域名数量
func (s *Storage) GetGlobalCount() int {
	return s.globalDomains.Count()
}

// Close 关闭存储（保存最终的主文件）
func (s *Storage) Close() error {
	return s.SaveMaster()
}
