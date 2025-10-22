package detector

import (
	"testing"
)

// 模拟 WAT 记录数据（包含 WordPress API）
var watRecordWithWP = []byte(`{
  "Envelope": {
    "WARC-Header-Metadata": {
      "WARC-Type": "metadata",
      "WARC-Target-URI": "https://example.com"
    },
    "Payload-Metadata": {
      "HTTP-Response-Metadata": {
        "Headers": {
          "Link": "<https://example.com/wp-json/>; rel=\"https://api.w.org/\""
        },
        "HTML-Metadata": {
          "Links": [
            {
              "url": "https://example.com/wp-json/",
              "rel": "https://api.w.org/"
            }
          ]
        }
      }
    }
  }
}`)

// 模拟 WAT 记录数据（不包含 WordPress API）
var watRecordWithoutWP = []byte(`{
  "Envelope": {
    "WARC-Header-Metadata": {
      "WARC-Type": "metadata",
      "WARC-Target-URI": "https://example.com"
    },
    "Payload-Metadata": {
      "HTTP-Response-Metadata": {
        "Headers": {},
        "HTML-Metadata": {
          "Links": []
        }
      }
    }
  }
}`)

// BenchmarkQuickCheck 测试快速检查性能
func BenchmarkQuickCheck(b *testing.B) {
	detector := NewDetector([]string{"api.w.org", "wp/v2", "wp-json"}, true)

	b.Run("WithMatch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			detector.quickCheck(watRecordWithWP)
		}
	})

	b.Run("WithoutMatch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			detector.quickCheck(watRecordWithoutWP)
		}
	})
}

// BenchmarkDetectInWATRecord 测试完整检测性能
func BenchmarkDetectInWATRecord(b *testing.B) {
	detector := NewDetector([]string{"api.w.org", "wp/v2", "wp-json"}, true)

	b.Run("WithMatch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			detector.DetectInWATRecord(watRecordWithWP)
		}
	})

	b.Run("WithoutMatch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			detector.DetectInWATRecord(watRecordWithoutWP)
		}
	})
}

// BenchmarkIsWPRestAPI 测试字符串检测性能
func BenchmarkIsWPRestAPI(b *testing.B) {
	detector := NewDetector([]string{"api.w.org", "wp/v2", "wp-json"}, true)
	testURL := "https://example.com/wp-json/wp/v2/posts"

	b.Run("Match", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			detector.isWPRestAPI(testURL)
		}
	})

	b.Run("NoMatch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			detector.isWPRestAPI("https://example.com/index.html")
		}
	})
}
