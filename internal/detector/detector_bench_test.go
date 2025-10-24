package detector

import (
	"encoding/json"
	"testing"
)

// More complete WAT record for realistic benchmarking
var watRecordWithWP = []byte(`{
  "Container": {
    "Filename": "crawl-data/CC-MAIN-2025-38/segments/1234/wat/CC-MAIN.wat.gz",
    "Compressed": true,
    "Offset": "0",
    "Gzip": true
  },
  "Envelope": {
    "Format": "WARC/1.0",
    "WARC-Header-Length": "123",
    "WARC-Header-Metadata": {
      "WARC-Type": "metadata",
      "WARC-Target-URI": "https://example.wordpress.com/",
      "WARC-Date": "2025-01-01T00:00:00Z",
      "WARC-Record-ID": "<urn:uuid:12345>",
      "WARC-Refers-To": "<urn:uuid:67890>",
      "WARC-Block-Digest": "sha1:ABC123",
      "Content-Type": "application/json",
      "Content-Length": "1000"
    },
    "Payload-Metadata": {
      "Actual-Content-Type": "text/html",
      "HTTP-Response-Metadata": {
        "Response-Message": {
          "Status": "200",
          "Version": "1.1",
          "Reason": "OK"
        },
        "Headers": {
          "Link": "<https://example.com/wp-json/>; rel=\"https://api.w.org/\""
        },
        "HTML-Metadata": {
          "Head": {
            "Link": [
              {
                "url": "https://api.w.org/",
                "rel": "https://api.w.org/"
              },
              {
                "url": "https://example.wordpress.com/wp-json/",
                "rel": "alternate"
              }
            ],
            "Metas": [
              {
                "name": "HTML@/lang",
                "content": "en-US"
              }
            ],
            "Title": "WordPress Site"
          },
          "Links": [
            {
              "path": "FORM@/action",
              "url": "https://example.wordpress.com/wp-comments-post.php",
              "method": "post"
            }
          ]
        }
      }
    }
  }
}`)

var watRecordWithoutWP = []byte(`{
  "Container": {
    "Filename": "crawl-data/CC-MAIN-2025-38/segments/1234/wat/CC-MAIN.wat.gz"
  },
  "Envelope": {
    "WARC-Header-Metadata": {
      "WARC-Type": "metadata",
      "WARC-Target-URI": "https://example.com"
    },
    "Payload-Metadata": {
      "HTTP-Response-Metadata": {
        "Response-Message": {
          "Status": "200"
        },
        "Headers": {},
        "HTML-Metadata": {
          "Links": []
        }
      }
    }
  }
}`)

// BenchmarkQuickCheck tests the pre-filter performance
func BenchmarkQuickCheck(b *testing.B) {
	detector := NewDetector([]string{"api.w.org", "wp/v2", "wp-json"}, true)

	b.Run("WithMatch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			detector.quickCheck(watRecordWithWP)
		}
	})

	b.Run("WithoutMatch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			detector.quickCheck(watRecordWithoutWP)
		}
	})
}

// BenchmarkDetectInWATRecord tests complete detection performance
func BenchmarkDetectInWATRecord(b *testing.B) {
	detector := NewDetector([]string{"api.w.org", "wp/v2", "wp-json"}, true)

	b.Run("WithMatch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = detector.DetectInWATRecord(watRecordWithWP)
		}
	})

	b.Run("WithoutMatch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = detector.DetectInWATRecord(watRecordWithoutWP)
		}
	})
}

// BenchmarkJSONUnmarshal measures just the JSON parsing overhead
func BenchmarkJSONUnmarshal(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var watData WATRecord
		_ = json.Unmarshal(watRecordWithWP, &watData)
	}
}

// BenchmarkIsWPRestAPI tests string detection performance
func BenchmarkIsWPRestAPI(b *testing.B) {
	detector := NewDetector([]string{"api.w.org", "wp/v2", "wp-json"}, true)

	testURLs := []string{
		"https://api.w.org/",
		"https://example.com/wp-json/wp/v2/posts",
		"https://example.com/index.html",
		"https://wordpress.com/wp/v2/",
	}

	b.Run("SingleMatch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			detector.isWPRestAPI(testURLs[0])
		}
	})

	b.Run("SingleNoMatch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			detector.isWPRestAPI(testURLs[2])
		}
	})

	b.Run("MixedURLs", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, url := range testURLs {
				detector.isWPRestAPI(url)
			}
		}
	})
}

// BenchmarkOptimizedDetector tests the optimized implementation if available
func BenchmarkOptimizedDetector(b *testing.B) {
	// Test the optimized version if implemented
	detector := NewOptimizedDetector([]string{"api.w.org", "wp/v2", "wp-json"}, true)

	b.Run("WithMatch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = detector.DetectInWATRecordOptimized(watRecordWithWP)
		}
	})

	b.Run("WithoutMatch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = detector.DetectInWATRecordOptimized(watRecordWithoutWP)
		}
	})

	b.Run("CachedURLCheck", func(b *testing.B) {
		// Warm up cache
		testURL := "https://api.w.org/"
		detector.isWPRestAPICached(testURL)

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			detector.isWPRestAPICached(testURL)
		}
	})
}

// BenchmarkParallelProcessing tests concurrent processing performance
func BenchmarkParallelProcessing(b *testing.B) {
	detector := NewDetector([]string{"api.w.org", "wp/v2", "wp-json"}, true)
	optimized := NewOptimizedDetector([]string{"api.w.org", "wp/v2", "wp-json"}, true)

	b.Run("OriginalParallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_, _ = detector.DetectInWATRecord(watRecordWithWP)
			}
		})
	})

	b.Run("OptimizedParallel", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_, _ = optimized.DetectInWATRecordOptimized(watRecordWithWP)
			}
		})
	})
}

// TestOptimizedCorrectness verifies the optimized version produces correct results
func TestOptimizedCorrectness(t *testing.T) {
	original := NewDetector([]string{"api.w.org", "wp/v2", "wp-json"}, true)
	optimized := NewOptimizedDetector([]string{"api.w.org", "wp/v2", "wp-json"}, true)

	testCases := []struct {
		name   string
		record []byte
	}{
		{"WithWordPress", watRecordWithWP},
		{"WithoutWordPress", watRecordWithoutWP},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hit1, err1 := original.DetectInWATRecord(tc.record)
			hit2, err2 := optimized.DetectInWATRecordOptimized(tc.record)

			if err1 != err2 {
				t.Errorf("Error mismatch: original=%v, optimized=%v", err1, err2)
			}

			if hit1 == nil && hit2 == nil {
				return // Both found nothing, OK
			}

			if hit1 == nil || hit2 == nil {
				t.Errorf("Hit mismatch: original=%v, optimized=%v", hit1 != nil, hit2 != nil)
				return
			}

			// Compare key fields
			if hit1.URL != hit2.URL {
				t.Errorf("URL mismatch: original=%s, optimized=%s", hit1.URL, hit2.URL)
			}

			if hit1.HasCommentForm != hit2.HasCommentForm {
				t.Errorf("HasCommentForm mismatch: original=%v, optimized=%v",
					hit1.HasCommentForm, hit2.HasCommentForm)
			}
		})
	}
}

// TestCacheEffectiveness tests the cache hit rate
func TestCacheEffectiveness(t *testing.T) {
	detector := NewOptimizedDetector([]string{"api.w.org", "wp/v2", "wp-json"}, false)

	testURLs := []string{
		"https://api.w.org/",
		"https://example.com/wp-json/",
		"https://example.com/",
		"https://wordpress.com/wp/v2/",
	}

	// First pass - cache miss
	for _, url := range testURLs {
		detector.isWPRestAPICached(url)
	}

	// Multiple passes - should hit cache
	for i := 0; i < 100; i++ {
		for _, url := range testURLs {
			detector.isWPRestAPICached(url)
		}
	}

	stats := detector.GetCacheStats()
	hitRate := stats["hit_rate_pct"]

	if hitRate < 95 {
		t.Errorf("Cache hit rate too low: %d%% (expected > 95%%)", hitRate)
	}

	t.Logf("Cache stats: hits=%d, misses=%d, hit_rate=%d%%",
		stats["cache_hits"], stats["cache_misses"], hitRate)
}