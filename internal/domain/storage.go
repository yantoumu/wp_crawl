package domain

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"
)

// Storage 域名存储管理器
type Storage struct {
	outputDir     string            // 输出目录
	rawDir        string            // 原始域名目录（每个文件一个）
	masterFile    string            // 主域名文件（全局去重）
	logger        *zap.Logger       // 日志记录器
	globalDomains map[string]string // 全局域名->语言映射（用于去重）
	mu            sync.RWMutex      // 保护全局域名集合
}

// NewStorage 创建新的域名存储管理器
func NewStorage(outputDir string, logger *zap.Logger) (*Storage, error) {
	// 创建输出目录结构
	rawDir := filepath.Join(outputDir, "raw")
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create raw directory: %w", err)
	}

	masterFile := filepath.Join(outputDir, "domains.txt")

	s := &Storage{
		outputDir:     outputDir,
		rawDir:        rawDir,
		masterFile:    masterFile,
		logger:        logger,
		globalDomains: make(map[string]string),
	}

	// 加载已有的主域名文件（如果存在）
	if err := s.loadMasterFile(); err != nil {
		logger.Warn("Failed to load existing master file", zap.Error(err))
	}

	return s, nil
}

// SaveBatch 保存一个批次的域名（处理完一个 WAT 文件）
// fileName: WAT 文件名（用于命名输出文件）
// domainLangPairs: 该文件提取的域名-语言对列表（格式：domain,language）
func (s *Storage) SaveBatch(fileName string, domainLangPairs []string) error {
	// 更新全局域名集合（即使没有域名也要更新）
	s.updateGlobal(domainLangPairs)

	// 生成标记文件名（基于 WAT 文件名）
	baseName := filepath.Base(fileName)
	baseName = strings.TrimSuffix(baseName, ".warc.wat.gz")
	baseName = strings.TrimSuffix(baseName, ".warc.gz")
	markerFile := filepath.Join(s.rawDir, baseName+".txt")

	// 创建空标记文件（用于断点续传检测）
	// 不写入实际数据，所有数据在主文件 domains.txt 中
	file, err := os.Create(markerFile)
	if err != nil {
		return fmt.Errorf("failed to create marker file: %w", err)
	}
	file.Close()

	return nil
}

// updateGlobal 更新全局域名集合
func (s *Storage) updateGlobal(domainLangPairs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, pair := range domainLangPairs {
		// 解析 domain,language,hasForm 格式（兼容旧格式 domain,language）
		parts := strings.SplitN(pair, ",", 3)
		if len(parts) >= 2 {
			domain := parts[0]
			language := parts[1]

			// 如果域名不存在，或者新的语言不为空而旧的为空，则更新
			if existingLang, exists := s.globalDomains[domain]; !exists || (language != "" && existingLang == "") {
				s.globalDomains[domain] = language
			}
		}
	}
}

// SaveMaster 保存主域名文件（全局去重后的所有域名）
func (s *Storage) SaveMaster() error {
	s.mu.RLock()
	domainLangPairs := make([]string, 0, len(s.globalDomains))
	for domain, language := range s.globalDomains {
		// 格式: domain,language (判断规则不变，只是保存格式简化)
		pair := domain + "," + language
		domainLangPairs = append(domainLangPairs, pair)
	}
	s.mu.RUnlock()

	if len(domainLangPairs) == 0 {
		s.logger.Warn("No domains to save to master file")
		return nil
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

	s.mu.Lock()
	defer s.mu.Unlock()

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
				s.globalDomains[domain] = language
			} else {
				// 兼容旧格式（只有域名）
				s.globalDomains[line] = ""
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

// GetGlobalCount 获取全局唯一域名数量
func (s *Storage) GetGlobalCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.globalDomains)
}

// Close 关闭存储（保存最终的主文件）
func (s *Storage) Close() error {
	return s.SaveMaster()
}
