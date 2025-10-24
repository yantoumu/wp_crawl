// Package config 提供配置管理功能
package config

import (
	"time"
)

// Config 定义扫描器的配置结构
type Config struct {
	// 爬虫批次设置
	Crawl struct {
		Name      string   `mapstructure:"name"`      // 爬虫批次名称，如 CC-MAIN-2025-38
		Segments  []string `mapstructure:"segments"`  // 要处理的段列表，空则处理所有
		StartFrom int      `mapstructure:"start_from"` // 从第几个WAT文件开始
	} `mapstructure:"crawl"`

	// 并发控制
	Concurrency struct {
		Workers       int `mapstructure:"workers"`        // 并发工作者数量
		DownloadQueue int `mapstructure:"download_queue"` // 下载队列大小
		ProcessQueue  int `mapstructure:"process_queue"`  // 处理队列大小
	} `mapstructure:"concurrency"`

	// 网络设置
	Network struct {
		Timeout              time.Duration `mapstructure:"timeout"`                 // HTTP超时时间
		RetryAttempts        int           `mapstructure:"retry_attempts"`          // 重试次数
		RetryDelay           time.Duration `mapstructure:"retry_delay"`             // 重试延迟
		UserAgent            string        `mapstructure:"user_agent"`              // User-Agent
		MaxConcurrentConns   int           `mapstructure:"max_concurrent_conns"`    // 最大并发HTTP连接数
	} `mapstructure:"network"`

	// 检测设置
	Detection struct {
		Patterns      []string `mapstructure:"patterns"`       // 检测模式列表
		EnableCDX     bool     `mapstructure:"enable_cdx"`     // 是否启用CDX验证
		CDXTimeout    time.Duration `mapstructure:"cdx_timeout"` // CDX查询超时
		PreFilter     bool     `mapstructure:"pre_filter"`     // 是否启用预过滤
	} `mapstructure:"detection"`

	// 输出设置
	Output struct {
		Directory      string `mapstructure:"directory"`       // 输出目录
		FilePrefix     string `mapstructure:"file_prefix"`     // 文件前缀
		Compress       bool   `mapstructure:"compress"`        // 是否压缩
		BufferSize     int    `mapstructure:"buffer_size"`     // 写入缓冲大小
		FlushInterval  time.Duration `mapstructure:"flush_interval"` // 刷新间隔
	} `mapstructure:"output"`

	// 断点续扫
	Resume struct {
		Enable        bool   `mapstructure:"enable"`         // 是否启用断点续扫
		StateFile     string `mapstructure:"state_file"`     // 状态文件路径
		SaveInterval  time.Duration `mapstructure:"save_interval"` // 保存间隔
	} `mapstructure:"resume"`

	// 日志设置
	Logging struct {
		LogLevel      string `mapstructure:"log_level"`      // 日志级别
		LogFile       string `mapstructure:"log_file"`       // 日志文件
		ProgressBar   bool   `mapstructure:"progress_bar"`   // 是否显示进度条
	} `mapstructure:"logging"`

	// 性能优化
	Performance struct {
		MaxMemoryMB   int  `mapstructure:"max_memory_mb"`   // 最大内存使用(MB)
		EnableProfile bool `mapstructure:"enable_profile"`  // 是否启用性能分析
		ProfilePort   int  `mapstructure:"profile_port"`    // 性能分析端口
	} `mapstructure:"performance"`

	// 外部API导出
	Exporter struct {
		Enable    bool          `mapstructure:"enable"`     // 是否启用外部API导出
		APIURL    string        `mapstructure:"api_url"`    // API端点URL
		APIKey    string        `mapstructure:"api_key"`    // API密钥
		BatchSize int           `mapstructure:"batch_size"` // 批量大小
		Timeout   time.Duration `mapstructure:"timeout"`    // 请求超时
	} `mapstructure:"exporter"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Crawl: struct {
			Name      string   `mapstructure:"name"`
			Segments  []string `mapstructure:"segments"`
			StartFrom int      `mapstructure:"start_from"`
		}{
			Name:      "CC-MAIN-2025-38",
			Segments:  []string{},
			StartFrom: 0,
		},
		Concurrency: struct {
			Workers       int `mapstructure:"workers"`
			DownloadQueue int `mapstructure:"download_queue"`
			ProcessQueue  int `mapstructure:"process_queue"`
		}{
			Workers:       1,  // 默认串行下载
			DownloadQueue: 50,
			ProcessQueue:  100,
		},
		Network: struct {
			Timeout              time.Duration `mapstructure:"timeout"`
			RetryAttempts        int           `mapstructure:"retry_attempts"`
			RetryDelay           time.Duration `mapstructure:"retry_delay"`
			UserAgent            string        `mapstructure:"user_agent"`
			MaxConcurrentConns   int           `mapstructure:"max_concurrent_conns"`
		}{
			Timeout:            30 * time.Second,
			RetryAttempts:      3,
			RetryDelay:         2 * time.Second,
			UserAgent:          "WP-Scanner/1.0 (WordPress API Discovery)",
			MaxConcurrentConns: 10,  // 默认10个并发连接（保守值，避免网络错误）
		},
		Detection: struct {
			Patterns      []string      `mapstructure:"patterns"`
			EnableCDX     bool          `mapstructure:"enable_cdx"`
			CDXTimeout    time.Duration `mapstructure:"cdx_timeout"`
			PreFilter     bool          `mapstructure:"pre_filter"`
		}{
			Patterns:      []string{"api.w.org", "wp/v2", "wp-json"},
			EnableCDX:     false,
			CDXTimeout:    10 * time.Second,
			PreFilter:     true,
		},
		Output: struct {
			Directory      string        `mapstructure:"directory"`
			FilePrefix     string        `mapstructure:"file_prefix"`
			Compress       bool          `mapstructure:"compress"`
			BufferSize     int           `mapstructure:"buffer_size"`
			FlushInterval  time.Duration `mapstructure:"flush_interval"`
		}{
			Directory:      ".",
			FilePrefix:     "hits",
			Compress:       true,
			BufferSize:     1024 * 1024, // 1MB
			FlushInterval:  10 * time.Second,
		},
		Resume: struct {
			Enable        bool          `mapstructure:"enable"`
			StateFile     string        `mapstructure:"state_file"`
			SaveInterval  time.Duration `mapstructure:"save_interval"`
		}{
			Enable:        true,
			StateFile:     "scanner_state.json",
			SaveInterval:  30 * time.Second,
		},
		Logging: struct {
			LogLevel      string `mapstructure:"log_level"`
			LogFile       string `mapstructure:"log_file"`
			ProgressBar   bool   `mapstructure:"progress_bar"`
		}{
			LogLevel:      "info",
			LogFile:       "scanner.log",
			ProgressBar:   true,
		},
		Performance: struct {
			MaxMemoryMB   int  `mapstructure:"max_memory_mb"`
			EnableProfile bool `mapstructure:"enable_profile"`
			ProfilePort   int  `mapstructure:"profile_port"`
		}{
			MaxMemoryMB:   2048,
			EnableProfile: false,
			ProfilePort:   6060,
		},
		Exporter: struct {
			Enable    bool          `mapstructure:"enable"`
			APIURL    string        `mapstructure:"api_url"`
			APIKey    string        `mapstructure:"api_key"`
			BatchSize int           `mapstructure:"batch_size"`
			Timeout   time.Duration `mapstructure:"timeout"`
		}{
			Enable:    false,
			APIURL:    "http://w.seo9.org:5001/api/external-links/pending/batch-import",
			APIKey:    "test_key_123",
			BatchSize: 500,
			Timeout:   30 * time.Second,
		},
	}
}

// Validate 验证配置的有效性
func (c *Config) Validate() error {
	// TODO: 实现配置验证逻辑
	return nil
}