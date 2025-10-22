// Package detector 提供WordPress REST API检测功能
package detector

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"
)

// HitSource 定义命中来源类型
type HitSource string

const (
	SourceHTMLLink HitSource = "html_link"
	SourceHTTPLink HitSource = "http_link"
	SourceJSONAPI  HitSource = "json_api"
)

// Hit 表示一个检测命中结果
type Hit struct {
	URL            string    `json:"url"`
	Timestamp      time.Time `json:"timestamp"`
	Source         HitSource `json:"source"`
	WATLocation    string    `json:"wat_location"`
	Segment        string    `json:"segment"`
	CrawlID        string    `json:"crawl_id"`
	Language       string    `json:"language,omitempty"`        // 页面语言（从 HTML@/lang 提取）
	HasCommentForm bool      `json:"has_comment_form"`          // 是否存在WordPress评论表单
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// Detector 负责检测WordPress REST API
type Detector struct {
	patterns       []string
	patternsLower  []string  // 预转换的小写版本
	patternsBytes  [][]byte  // 预转换的 []byte 版本
	preFilter      bool
	minLength      int

	// Debug counters (using atomic for thread-safety)
	totalRecords    atomic.Int64
	quickCheckPass  atomic.Int64
	parseSuccess    atomic.Int64
	hasPayload      atomic.Int64
	checkedHTML     atomic.Int64
	checkedHTTP     atomic.Int64
}

// NewDetector 创建新的检测器实例
func NewDetector(patterns []string, preFilter bool) *Detector {
	if len(patterns) == 0 {
		patterns = []string{"api.w.org", "wp/v2", "wp-json"}
	}

	// 预转换 patterns 到小写和 []byte 格式（缓存优化）
	patternsLower := make([]string, len(patterns))
	patternsBytes := make([][]byte, len(patterns))

	minLength := len(patterns[0])
	for i, p := range patterns {
		patternsLower[i] = strings.ToLower(p)
		patternsBytes[i] = []byte(p)

		if len(p) < minLength {
			minLength = len(p)
		}
	}

	return &Detector{
		patterns:      patterns,
		patternsLower: patternsLower,
		patternsBytes: patternsBytes,
		preFilter:     preFilter,
		minLength:     minLength,
	}
}

// DetectInWATRecord 在WAT记录中检测WordPress API
func (d *Detector) DetectInWATRecord(record []byte) (*Hit, error) {
	d.totalRecords.Add(1)

	// 快速预过滤
	if d.preFilter {
		if !d.quickCheck(record) {
			return nil, nil  // 快速检查未通过
		}
		d.quickCheckPass.Add(1)
	}

	// 解析WAT记录
	var watData WATRecord
	if err := json.Unmarshal(record, &watData); err != nil {
		return nil, err
	}
	d.parseSuccess.Add(1)

	// Track if has payload
	if watData.Envelope.PayloadMetadata != nil {
		d.hasPayload.Add(1)
	}

	// 检查HTML链接
	d.checkedHTML.Add(1)
	if hit := d.checkHTMLLinks(watData); hit != nil {
		return hit, nil
	}

	// 检查HTTP头部链接
	d.checkedHTTP.Add(1)
	if hit := d.checkHTTPLinks(watData); hit != nil {
		return hit, nil
	}

	// 检查JSON-LD等其他元数据
	if hit := d.checkMetadata(watData); hit != nil {
		return hit, nil
	}

	return nil, nil
}

// GetStats returns debug statistics
func (d *Detector) GetStats() map[string]int64 {
	return map[string]int64{
		"total_records":    d.totalRecords.Load(),
		"quick_check_pass": d.quickCheckPass.Load(),
		"parse_success":    d.parseSuccess.Load(),
		"has_payload":      d.hasPayload.Load(),
		"checked_html":     d.checkedHTML.Load(),
		"checked_http":     d.checkedHTTP.Load(),
	}
}

// quickCheck 快速检查是否可能包含目标模式
func (d *Detector) quickCheck(data []byte) bool {
	// 使用 bytes.Contains 避免 string 转换（零拷贝优化）
	for _, pattern := range d.patternsBytes {
		if bytes.Contains(data, pattern) {
			return true
		}
	}
	return false
}

// hasWordPressCommentForm 检查是否存在WordPress评论表单
// 检查 HTMLMetadata.Links 中是否有 wp-comments-post.php 表单
func hasWordPressCommentForm(links interface{}) bool {
	linkList, ok := links.([]interface{})
	if !ok {
		return false
	}

	for _, link := range linkList {
		linkMap, ok := link.(map[string]interface{})
		if !ok {
			continue
		}

		// 检查是否是表单提交
		path, _ := linkMap["path"].(string)
		url, _ := linkMap["url"].(string)
		method, _ := linkMap["method"].(string)

		// WordPress评论表单特征: path=FORM@/action, url包含wp-comments-post.php, method=post
		if path == "FORM@/action" &&
		   strings.Contains(url, "wp-comments-post.php") &&
		   method == "post" {
			return true
		}
	}

	return false
}

// extractLanguage 从 HTML Metas 中提取语言
func extractLanguage(metas interface{}) string {
	metaList, ok := metas.([]interface{})
	if !ok {
		return ""
	}

	for _, meta := range metaList {
		metaMap, ok := meta.(map[string]interface{})
		if !ok {
			continue
		}

		// 查找 name="HTML@/lang" 的 meta 标签
		if name, ok := metaMap["name"].(string); ok && name == "HTML@/lang" {
			if content, ok := metaMap["content"].(string); ok {
				return content
			}
		}
	}

	return ""
}

// checkHTMLLinks 检查HTML链接标签
func (d *Detector) checkHTMLLinks(data WATRecord) *Hit {
	// 检查HTML链接元数据
	if data.Envelope.PayloadMetadata == nil || data.Envelope.PayloadMetadata.HTTPResponseMetadata == nil {
		return nil
	}

	// ✅ 检查 HTTP Status 必须是 200
	status := data.Envelope.PayloadMetadata.HTTPResponseMetadata.ResponseMessage.Status
	if status != "200" {
		return nil
	}

	// 尝试从 Head.Link 读取（正确路径）
	links, ok := data.Envelope.PayloadMetadata.HTTPResponseMetadata.HTMLMetadata.Head.Link.([]interface{})
	if !ok {
		// 回退到旧路径（兼容性）
		links, ok = data.Envelope.PayloadMetadata.HTTPResponseMetadata.HTMLMetadata.Links.([]interface{})
		if !ok {
			// 调试：记录Link字段的实际类型
			if data.Envelope.PayloadMetadata.HTTPResponseMetadata.HTMLMetadata.Head.Link != nil {
				// Link 字段存在但不是数组，跳过
			}
			return nil
		}
	}

	// 检查是否包含 api.w.org
	hasAPI := false
	for _, link := range links {
		linkMap, ok := link.(map[string]interface{})
		if !ok {
			continue
		}

		// 检查链接URL和rel属性
		url, _ := linkMap["url"].(string)
		rel, _ := linkMap["rel"].(string)

		if d.isWPRestAPI(url) || (rel == "https://api.w.org/" || strings.Contains(rel, "wp-json")) {
			hasAPI = true
			break
		}
	}

	// 如果没有找到 api.w.org，直接返回
	if !hasAPI {
		return nil
	}

	// 检查是否存在WordPress评论表单（必须存在才保存）
	if !hasWordPressCommentForm(data.Envelope.PayloadMetadata.HTTPResponseMetadata.HTMLMetadata.Links) {
		return nil
	}

	// 提取语言
	language := extractLanguage(data.Envelope.PayloadMetadata.HTTPResponseMetadata.HTMLMetadata.Head.Metas)
	if language == "" {
		language = "unknown"
	}

	return &Hit{
		URL:            data.Envelope.WARCHeaderMetadata.WARCTargetURI,
		Timestamp:      time.Now(),
		Source:         SourceHTMLLink,
		WATLocation:    data.Container.Filename,
		Segment:        extractSegment(data.Container.Filename),
		CrawlID:        extractCrawlID(data.Container.Filename),
		Language:       language,
		HasCommentForm: true, // 表单存在
		Metadata:       map[string]interface{}{},
	}
}

// checkHTTPLinks 检查HTTP Link头部
func (d *Detector) checkHTTPLinks(data WATRecord) *Hit {
	if data.Envelope.PayloadMetadata == nil || data.Envelope.PayloadMetadata.HTTPResponseMetadata == nil {
		return nil
	}

	// ✅ 检查 HTTP Status 必须是 200
	status := data.Envelope.PayloadMetadata.HTTPResponseMetadata.ResponseMessage.Status
	if status != "200" {
		return nil
	}

	// 检查响应头中的Link字段（注意：可能是 "Link" 或 "link"）
	headers := data.Envelope.PayloadMetadata.HTTPResponseMetadata.Headers

	// 尝试大写 Link
	linkHeader, ok := headers["Link"]
	if !ok {
		// 尝试小写 link
		linkHeader, ok = headers["link"]
	}

	if ok {
		// 检查是否包含 api.w.org
		hasAPI := false
		switch v := linkHeader.(type) {
		case string:
			if d.isWPRestAPI(v) {
				hasAPI = true
			}
		case []interface{}:
			for _, item := range v {
				if linkStr, ok := item.(string); ok && d.isWPRestAPI(linkStr) {
					hasAPI = true
					break
				}
			}
		}

		// 如果没有找到 api.w.org，直接返回
		if !hasAPI {
			return nil
		}

		// 检查是否存在WordPress评论表单（必须存在才保存）
		if !hasWordPressCommentForm(data.Envelope.PayloadMetadata.HTTPResponseMetadata.HTMLMetadata.Links) {
			return nil
		}

		// 提取语言
		language := extractLanguage(data.Envelope.PayloadMetadata.HTTPResponseMetadata.HTMLMetadata.Head.Metas)
		if language == "" {
			language = "unknown"
		}

		return &Hit{
			URL:            data.Envelope.WARCHeaderMetadata.WARCTargetURI,
			Timestamp:      time.Now(),
			Source:         SourceHTTPLink,
			WATLocation:    data.Container.Filename,
			Segment:        extractSegment(data.Container.Filename),
			CrawlID:        extractCrawlID(data.Container.Filename),
			Language:       language,
			HasCommentForm: true, // 表单存在
			Metadata:       map[string]interface{}{},
		}
	}

	return nil
}

// checkMetadata 检查其他元数据
func (d *Detector) checkMetadata(data WATRecord) *Hit {
	// 可以扩展检查JSON-LD、Schema.org等其他元数据
	return nil
}

// isWPRestAPI 检查是否包含WordPress REST API标识
func (d *Detector) isWPRestAPI(text string) bool {
	if text == "" {
		return false
	}

	textLower := strings.ToLower(text)
	// 使用预转换的小写 patterns（缓存优化）
	for _, pattern := range d.patternsLower {
		if strings.Contains(textLower, pattern) {
			return true
		}
	}
	return false
}

// extractSegment 从文件路径提取段名称
func extractSegment(filename string) string {
	parts := strings.Split(filename, "/")
	for i, part := range parts {
		if part == "segments" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// extractCrawlID 从文件路径提取爬虫ID
func extractCrawlID(filename string) string {
	parts := strings.Split(filename, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, "CC-MAIN-") {
			return part
		}
	}
	return ""
}

// WATRecord 表示WAT文件中的记录结构
// Fixed based on actual Common Crawl WAT JSON structure
type WATRecord struct {
	Container struct {
		Filename   string `json:"Filename"`   // Fixed: Capital F
		Compressed bool   `json:"Compressed"` // Added: exists in real data
		Offset     string `json:"Offset"`     // Fixed: Capital O
		Gzip       bool   `json:"Gzip"`       // Fixed: Capital G
	} `json:"Container"`

	Envelope struct {
		Format              string `json:"Format"`
		WARCHeaderLength    string `json:"WARC-Header-Length"` // Added: missing field
		WARCHeaderMetadata struct {
			WARCType         string    `json:"WARC-Type"`
			WARCTargetURI    string    `json:"WARC-Target-URI"`
			WARCDate         time.Time `json:"WARC-Date"`
			WARCRecordID     string    `json:"WARC-Record-ID"`
			WARCRefersTo     string    `json:"WARC-Refers-To"`
			WARCBlockDigest  string    `json:"WARC-Block-Digest"`
			ContentType      string    `json:"Content-Type"`
			ContentLength    string    `json:"Content-Length"` // Fixed: string not int
		} `json:"WARC-Header-Metadata"`

		PayloadMetadata *struct {
			ActualContentType string `json:"Actual-Content-Type"` // Present in all payload records
			HTTPResponseMetadata *struct {
				ResponseMessage struct {
					Status  string `json:"Status"`  // HTTP 状态码（如 "200"）
					Version string `json:"Version"` // HTTP 版本
					Reason  string `json:"Reason"`  // 状态描述（如 "OK"）
				} `json:"Response-Message"`
				ResponseHeaders map[string]interface{} `json:"Response-Headers"`
				HeadersLength   string                 `json:"Headers-Length"` // Added: missing field
				Headers         map[string]interface{} `json:"Headers"`
				HTMLMetadata    struct {
					Head struct {
						Link    interface{} `json:"Link"`
						Metas   interface{} `json:"Metas"`
						Title   string      `json:"Title"`
						Scripts interface{} `json:"Scripts"`
					} `json:"Head"`
					Links interface{} `json:"Links"`  // 保留兼容性
				} `json:"HTML-Metadata"`
			} `json:"HTTP-Response-Metadata"`
		} `json:"Payload-Metadata"`
	} `json:"Envelope"`
}