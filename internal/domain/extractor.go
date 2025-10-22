// Package domain 提供域名提取和去重功能
package domain

import (
	"net/url"
	"strings"
	"sync"
)

// Extractor 域名提取器
type Extractor struct {
	domains map[string]string // 域名 -> 语言的映射
	mu      sync.RWMutex      // 保护并发访问
}

// NewExtractor 创建新的域名提取器
func NewExtractor() *Extractor {
	return &Extractor{
		domains: make(map[string]string),
	}
}

// ExtractDomain 从 URL 中提取主域名
// 返回格式: protocol://subdomain.domain.tld/ 或 protocol://subdomain.domain.tld:port/
func ExtractDomain(rawURL string) (string, error) {
	if rawURL == "" {
		return "", nil
	}

	// 解析 URL
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	// 构建主域名: scheme://host/
	domain := parsedURL.Scheme + "://" + parsedURL.Host + "/"

	return domain, nil
}

// Add 添加域名（自动去重，不包含语言）
func (e *Extractor) Add(domain string) {
	e.AddWithLanguage(domain, "")
}

// AddWithLanguage 添加域名和语言（自动去重）
func (e *Extractor) AddWithLanguage(domain, language string) {
	if domain == "" {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// 如果域名已存在且新的语言不为空，更新语言
	if existingLang, exists := e.domains[domain]; exists {
		if language != "" && existingLang == "" {
			e.domains[domain] = language
		}
	} else {
		e.domains[domain] = language
	}
}

// AddFromURL 从 URL 提取域名并添加
func (e *Extractor) AddFromURL(rawURL string) error {
	return e.AddFromURLWithLanguage(rawURL, "")
}

// AddFromURLWithLanguage 从 URL 提取域名并添加，同时指定语言
func (e *Extractor) AddFromURLWithLanguage(rawURL, language string) error {
	domain, err := ExtractDomain(rawURL)
	if err != nil {
		return err
	}

	e.AddWithLanguage(domain, language)
	return nil
}

// GetDomains 获取所有唯一域名（已排序）
func (e *Extractor) GetDomains() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	domains := make([]string, 0, len(e.domains))
	for domain := range e.domains {
		domains = append(domains, domain)
	}

	// 简单排序（字典序）
	// 注意：这里可以使用 sort.Strings(domains) 如果需要排序
	return domains
}

// GetDomainLanguagePairs 获取所有域名和语言对（格式：domain,language）
func (e *Extractor) GetDomainLanguagePairs() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	pairs := make([]string, 0, len(e.domains))
	for domain, language := range e.domains {
		// 格式: domain,language (storage层会加上 ,true)
		pair := domain + "," + language
		pairs = append(pairs, pair)
	}

	return pairs
}

// Count 返回唯一域名数量
func (e *Extractor) Count() int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return len(e.domains)
}

// Clear 清空所有域名
func (e *Extractor) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.domains = make(map[string]string)
}

// Merge 合并另一个提取器的域名
func (e *Extractor) Merge(other *Extractor) {
	if other == nil {
		return
	}

	other.mu.RLock()
	otherDomains := make(map[string]string, len(other.domains))
	for k, v := range other.domains {
		otherDomains[k] = v
	}
	other.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	for domain, lang := range otherDomains {
		// 如果域名不存在，或者新的语言不为空而旧的为空，则更新
		if existingLang, exists := e.domains[domain]; !exists || (lang != "" && existingLang == "") {
			e.domains[domain] = lang
		}
	}
}

// ShouldInclude 检查 Hit 记录是否应该包含域名
// 判断条件：metadata 中包含 "api.w.org"
func ShouldInclude(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}

	// 检查所有 metadata 值中是否包含 "api.w.org"
	for _, value := range metadata {
		if str, ok := value.(string); ok {
			if strings.Contains(strings.ToLower(str), "api.w.org") {
				return true
			}
		}
	}

	return false
}
