// detector_manual.go - 手动解析版本（零JSON解析，参考C++实现）
package detector

import (
	"bytes"
	"time"
)

// DetectInWATRecordManual 手动解析WAT记录（零JSON解析优化）
// 参考C++的inline字符串提取，避免encoding/json的反射开销
func (d *Detector) DetectInWATRecordManual(record []byte) (*Hit, error) {
	d.totalRecords.Add(1)

	// ⚡ 第1层过滤: Status 200检查（过滤~80%记录）
	hasStatus200 := bytes.Contains(record, []byte(`"Status":"200"`))
	if !hasStatus200 {
		return nil, nil
	}

	// ⚡ 第2层过滤: WordPress特征检查（过滤~19%记录）
	if d.preFilter {
		if !d.quickCheck(record) {
			return nil, nil
		}
		d.quickCheckPass.Add(1)
	}

	// ⚡ 第3层过滤: 必须同时包含 api.w.org 和 wp-comments-post.php
	// 这是最终确认是WordPress站点的关键证据
	hasAPIWorg := bytes.Contains(record, []byte(`api.w.org`))
	if !hasAPIWorg {
		return nil, nil
	}

	// 检查是否有评论表单
	hasCommentForm := bytes.Contains(record, []byte(`wp-comments-post.php`))
	if !hasCommentForm {
		return nil, nil  // 没有评论表单，不是我们要的WordPress站点
	}

	// ⚡ 第4层: 快速提取URL（只提取必要字段）
	url := extractURLFast(record)
	if url == "" {
		return nil, nil
	}

	// ⚡ 第5层: 快速提取Filename
	filename := extractFilenameFast(record)

	// 统计计数
	d.parseSuccess.Add(1)
	d.hasPayload.Add(1)
	d.checkedHTML.Add(1)

	// 延迟提取语言（只在真正需要时）
	return &Hit{
		URL:            url,
		Timestamp:      time.Now(),
		Source:         SourceHTMLLink,
		WATLocation:    filename,
		Segment:        extractSegmentFast(filename),
		CrawlID:        extractCrawlIDFast(filename),
		Language:       "unknown", // 跳过语言提取（节省CPU）
		HasCommentForm: true,
		Metadata:       nil, // nil而不是空map（节省内存分配）
	}, nil
}

// ⚡ 极速提取函数 - 参考C++的inline实现，最小化开销

// extractURLFast 快速提取URL（内联优化）
func extractURLFast(data []byte) string {
	// 使用固定字节查找，避免[]byte分配
	const prefix = `"WARC-Target-URI":"`
	idx := bytes.Index(data, []byte(prefix))
	if idx == -1 {
		return ""
	}
	start := idx + len(prefix)

	// 快速查找结束引号
	for i := start; i < len(data); i++ {
		if data[i] == '"' {
			return string(data[start:i])
		}
	}
	return ""
}

// extractFilenameFast 快速提取文件名
func extractFilenameFast(data []byte) string {
	const prefix = `"Filename":"`
	idx := bytes.Index(data, []byte(prefix))
	if idx == -1 {
		return ""
	}
	start := idx + len(prefix)

	for i := start; i < len(data); i++ {
		if data[i] == '"' {
			return string(data[start:i])
		}
	}
	return ""
}

// extractSegmentFast 快速提取segment（避免strings.Split）
func extractSegmentFast(filename string) string {
	// 查找 "/segments/" 然后取下一段
	idx := bytes.Index([]byte(filename), []byte("/segments/"))
	if idx == -1 {
		return ""
	}
	start := idx + len("/segments/")

	// 查找下一个 /
	for i := start; i < len(filename); i++ {
		if filename[i] == '/' {
			return filename[start:i]
		}
	}
	return filename[start:]
}

// extractCrawlIDFast 快速提取crawl ID（避免strings.Split）
func extractCrawlIDFast(filename string) string {
	// 查找 "CC-MAIN-" 开头的段
	idx := bytes.Index([]byte(filename), []byte("CC-MAIN-"))
	if idx == -1 {
		return ""
	}

	// 查找这段的结束（下一个/）
	start := idx
	for i := start; i < len(filename); i++ {
		if filename[i] == '/' {
			return filename[start:i]
		}
	}
	return filename[start:]
}
