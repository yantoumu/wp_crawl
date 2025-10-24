package domain

import "testing"

func TestIsChineseLanguageCode(t *testing.T) {
	tests := []struct {
		name     string
		langCode string
		want     bool
	}{
		// 精确匹配测试
		{"zh", "zh", true},
		{"zh_CN", "zh_CN", true},
		{"zh-CN", "zh-CN", true},
		{"zh_cn", "zh_cn", true},
		{"zh-cn", "zh-cn", true},
		{"zh-Hans", "zh-Hans", true},
		{"zh-Hant", "zh-Hant", true},
		{"zh-cmn", "zh-cmn", true},
		{"zh-SG", "zh-SG", true},
		{"zh-HK", "zh-HK", true},
		{"zh-TW", "zh-TW", true},
		{"cmn", "cmn", true},

		// 前缀匹配测试
		{"zh-custom", "zh-custom", true},
		{"zh_custom", "zh_custom", true},
		{"cmn-custom", "cmn-custom", true},

		// 非中文语言代码
		{"en", "en", false},
		{"en-US", "en-US", false},
		{"ja", "ja", false},
		{"ko", "ko", false},
		{"fr", "fr", false},
		{"de", "de", false},
		{"empty", "", false},

		// 易混淆的测试
		{"zh in string", "some-zh-text", false}, // zh 不在开头
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isChineseLanguageCode(tt.langCode); got != tt.want {
				t.Errorf("isChineseLanguageCode(%q) = %v, want %v", tt.langCode, got, tt.want)
			}
		})
	}
}

func TestAddFromURLWithLanguage_ChineseFilter(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		language string
		wantAdded bool
	}{
		{
			name:     "非中文语言应该被添加",
			url:      "https://example.com/",
			language: "en",
			wantAdded: true,
		},
		{
			name:     "中文 zh-CN 应该被过滤",
			url:      "https://example.com/",
			language: "zh-CN",
			wantAdded: false,
		},
		{
			name:     "中文 zh_cn 应该被过滤",
			url:      "https://example.com/",
			language: "zh_cn",
			wantAdded: false,
		},
		{
			name:     "中文 zh-Hans 应该被过滤",
			url:      "https://example.com/",
			language: "zh-Hans",
			wantAdded: false,
		},
		{
			name:     "中文 zh-HK 应该被过滤",
			url:      "https://example.com/",
			language: "zh-HK",
			wantAdded: false,
		},
		{
			name:     "空语言应该被添加",
			url:      "https://example.com/",
			language: "",
			wantAdded: true,
		},
		{
			name:     "英文 en-US 应该被添加",
			url:      "https://example.com/",
			language: "en-US",
			wantAdded: true,
		},
		{
			name:     "日文 ja 应该被添加",
			url:      "https://example.com/",
			language: "ja",
			wantAdded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor := NewExtractor()
			err := extractor.AddFromURLWithLanguage(tt.url, tt.language)
			if err != nil {
				t.Fatalf("AddFromURLWithLanguage() error = %v", err)
			}

			count := extractor.Count()
			if tt.wantAdded && count != 1 {
				t.Errorf("域名应该被添加，但 Count() = %d, want 1", count)
			}
			if !tt.wantAdded && count != 0 {
				t.Errorf("域名不应该被添加（中文语言），但 Count() = %d, want 0", count)
			}
		})
	}
}
