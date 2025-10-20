// Package detector 提供WordPress REST API检测功能
package detector

import (
	"encoding/json"
	"strings"
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
	URL         string    `json:"url"`
	Timestamp   time.Time `json:"timestamp"`
	Source      HitSource `json:"source"`
	WATLocation string    `json:"wat_location"`
	Segment     string    `json:"segment"`
	CrawlID     string    `json:"crawl_id"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Detector 负责检测WordPress REST API
type Detector struct {
	patterns   []string
	preFilter  bool
	minLength  int
}

// NewDetector 创建新的检测器实例
func NewDetector(patterns []string, preFilter bool) *Detector {
	if len(patterns) == 0 {
		patterns = []string{"api.w.org", "wp/v2", "wp-json"}
	}

	// 计算最短模式长度用于预过滤
	minLength := len(patterns[0])
	for _, p := range patterns[1:] {
		if len(p) < minLength {
			minLength = len(p)
		}
	}

	return &Detector{
		patterns:  patterns,
		preFilter: preFilter,
		minLength: minLength,
	}
}

// DetectInWATRecord 在WAT记录中检测WordPress API
func (d *Detector) DetectInWATRecord(record []byte) (*Hit, error) {
	// 快速预过滤
	if d.preFilter && !d.quickCheck(record) {
		return nil, nil
	}

	// 解析WAT记录
	var watData WATRecord
	if err := json.Unmarshal(record, &watData); err != nil {
		return nil, err
	}

	// 检查HTML链接
	if hit := d.checkHTMLLinks(watData); hit != nil {
		return hit, nil
	}

	// 检查HTTP头部链接
	if hit := d.checkHTTPLinks(watData); hit != nil {
		return hit, nil
	}

	// 检查JSON-LD等其他元数据
	if hit := d.checkMetadata(watData); hit != nil {
		return hit, nil
	}

	return nil, nil
}

// quickCheck 快速检查是否可能包含目标模式
func (d *Detector) quickCheck(data []byte) bool {
	// 使用子串预筛选优化性能
	dataStr := string(data)
	for _, pattern := range d.patterns {
		if strings.Contains(dataStr, pattern) {
			return true
		}
	}
	return false
}

// checkHTMLLinks 检查HTML链接标签
func (d *Detector) checkHTMLLinks(data WATRecord) *Hit {
	// 检查HTML链接元数据
	if data.Envelope.PayloadMetadata == nil {
		return nil
	}

	links, ok := data.Envelope.PayloadMetadata.HTTPResponseMetadata.HTMLMetadata.Links.([]interface{})
	if !ok {
		return nil
	}

	for _, link := range links {
		linkMap, ok := link.(map[string]interface{})
		if !ok {
			continue
		}

		// 检查链接URL和rel属性
		url, _ := linkMap["url"].(string)
		rel, _ := linkMap["rel"].(string)

		if d.isWPRestAPI(url) || (rel == "https://api.w.org/" || strings.Contains(rel, "wp-json")) {
			return &Hit{
				URL:         data.Envelope.WARCHeaderMetadata.WARCTargetURI,
				Timestamp:   time.Now(),
				Source:      SourceHTMLLink,
				WATLocation: data.Container.Filename,
				Segment:     extractSegment(data.Container.Filename),
				CrawlID:     extractCrawlID(data.Container.Filename),
				Metadata: map[string]interface{}{
					"link_url": url,
					"link_rel": rel,
				},
			}
		}
	}

	return nil
}

// checkHTTPLinks 检查HTTP Link头部
func (d *Detector) checkHTTPLinks(data WATRecord) *Hit {
	if data.Envelope.PayloadMetadata == nil {
		return nil
	}

	// 检查响应头中的Link字段
	headers := data.Envelope.PayloadMetadata.HTTPResponseMetadata.Headers
	if linkHeader, ok := headers["Link"].(string); ok {
		if d.isWPRestAPI(linkHeader) {
			return &Hit{
				URL:         data.Envelope.WARCHeaderMetadata.WARCTargetURI,
				Timestamp:   time.Now(),
				Source:      SourceHTTPLink,
				WATLocation: data.Container.Filename,
				Segment:     extractSegment(data.Container.Filename),
				CrawlID:     extractCrawlID(data.Container.Filename),
				Metadata: map[string]interface{}{
					"link_header": linkHeader,
				},
			}
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
	for _, pattern := range d.patterns {
		if strings.Contains(textLower, strings.ToLower(pattern)) {
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
type WATRecord struct {
	Container struct {
		Filename    string `json:"filename"`
		Offset      string `json:"offset"`
		Gzip        bool   `json:"gzip"`
	} `json:"Container"`

	Envelope struct {
		Format              string `json:"Format"`
		WARCHeaderMetadata struct {
			WARCType         string    `json:"WARC-Type"`
			WARCTargetURI    string    `json:"WARC-Target-URI"`
			WARCDate         time.Time `json:"WARC-Date"`
			WARCRecordID     string    `json:"WARC-Record-ID"`
			WARCRefersTo     string    `json:"WARC-Refers-To"`
			WARCBlockDigest  string    `json:"WARC-Block-Digest"`
			ContentType      string    `json:"Content-Type"`
			ContentLength    int       `json:"Content-Length"`
		} `json:"WARC-Header-Metadata"`

		PayloadMetadata *struct {
			HTTPResponseMetadata struct {
				ResponseHeaders map[string]interface{} `json:"Response-Headers"`
				Headers         map[string]interface{} `json:"Headers"`
				HTMLMetadata    struct {
					Links    interface{} `json:"Links"`
					Metas    interface{} `json:"Metas"`
					Scripts  interface{} `json:"Scripts"`
				} `json:"HTML-Metadata"`
			} `json:"HTTP-Response-Metadata"`
		} `json:"Payload-Metadata,omitempty"`
	} `json:"Envelope"`
}