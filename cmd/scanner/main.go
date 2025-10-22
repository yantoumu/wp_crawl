// Package main 提供命令行入口
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"

	"github.com/yanyun/wp_crawl/internal/config"
	"github.com/yanyun/wp_crawl/internal/monitor"
	"github.com/yanyun/wp_crawl/internal/scanner"
)

var (
	cfgFile string
	cfg     *config.Config
	logger  *zap.Logger

	// 版本信息
	version = "1.0.0"
	commit  = "unknown"
	date    = "unknown"
)

// rootCmd 根命令
var rootCmd = &cobra.Command{
	Use:   "wp_crawl",
	Short: "WordPress REST API scanner for Common Crawl datasets",
	Long: `A high-performance scanner that processes Common Crawl WAT files
to discover websites with WordPress REST API endpoints.`,
}

// scanCmd 扫描命令
var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Start scanning Common Crawl data",
	Long:  `Start scanning Common Crawl WAT files to find WordPress sites.`,
	RunE:  runScan,
}

// versionCmd 版本命令
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("wp_crawl version %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
	},
}

// validateCmd 验证配置命令
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(nil); err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("config validation failed: %w", err)
		}

		fmt.Println("Configuration is valid!")
		return nil
	},
}

func init() {
	cobra.OnInitialize(initConfig)

	// 全局标志
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default: config.yaml)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().String("log-file", "", "log file path")

	// 扫描命令标志
	scanCmd.Flags().String("crawl", "", "crawl ID (e.g., CC-MAIN-2025-38)")
	scanCmd.Flags().StringSlice("segments", []string{}, "specific segments to process")
	scanCmd.Flags().Int("start-from", 0, "start from WAT file index")
	scanCmd.Flags().Int("workers", 10, "number of concurrent workers")
	scanCmd.Flags().Bool("no-resume", false, "disable resume from previous state")
	scanCmd.Flags().Bool("no-progress", false, "disable progress bar")
	scanCmd.Flags().Bool("cdx", false, "enable CDX validation")
	scanCmd.Flags().StringSlice("patterns", []string{"api.w.org", "wp/v2", "wp-json"}, "detection patterns")

	// 绑定标志到配置
	viper.BindPFlag("crawl.name", scanCmd.Flags().Lookup("crawl"))
	viper.BindPFlag("crawl.segments", scanCmd.Flags().Lookup("segments"))
	viper.BindPFlag("crawl.start_from", scanCmd.Flags().Lookup("start-from"))
	viper.BindPFlag("concurrency.workers", scanCmd.Flags().Lookup("workers"))
	viper.BindPFlag("detection.enable_cdx", scanCmd.Flags().Lookup("cdx"))
	viper.BindPFlag("detection.patterns", scanCmd.Flags().Lookup("patterns"))

	// 添加命令
	rootCmd.AddCommand(scanCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(validateCmd)
}

// initConfig 初始化配置
func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.AddConfigPath(".")
		viper.AddConfigPath("./configs")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	viper.AutomaticEnv()
	viper.SetEnvPrefix("WP_CRAWL")

	// 设置默认值
	cfg = config.DefaultConfig()

	// 读取配置文件
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}

// loadConfig 加载配置
func loadConfig(cmd *cobra.Command) error {
	cfg = config.DefaultConfig()

	if err := viper.Unmarshal(cfg); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}

	// 应用命令行标志覆盖
	if cmd != nil {
		if noResume, _ := cmd.Flags().GetBool("no-resume"); noResume {
			cfg.Resume.Enable = false
		}

		if noProgress, _ := cmd.Flags().GetBool("no-progress"); noProgress {
			cfg.Logging.ProgressBar = false
		}
	}

	return cfg.Validate()
}

// initLogger 初始化日志器
func initLogger() error {
	// 优先使用配置文件中的日志级别
	logLevel := cfg.Logging.LogLevel
	logFile := cfg.Logging.LogFile

	// 如果命令行显式设置了日志级别，则覆盖配置文件
	if rootCmd.PersistentFlags().Changed("log-level") {
		logLevel, _ = rootCmd.PersistentFlags().GetString("log-level")
	}
	if rootCmd.PersistentFlags().Changed("log-file") {
		logFile, _ = rootCmd.PersistentFlags().GetString("log-file")
	}

	var err error
	logger, err = monitor.NewLogger(logLevel, logFile)
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}

	return nil
}

// runScan 执行扫描
func runScan(cmd *cobra.Command, args []string) error {
	// 加载配置
	if err := loadConfig(cmd); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// 初始化日志
	if err := initLogger(); err != nil {
		return fmt.Errorf("failed to init logger: %w", err)
	}
	defer logger.Sync()

	// 验证必需参数
	if cfg.Crawl.Name == "" {
		return fmt.Errorf("crawl ID is required (use --crawl flag)")
	}

	logger.Info("Starting WordPress REST API scanner",
		zap.String("version", version),
		zap.String("crawl", cfg.Crawl.Name),
		zap.Int("workers", cfg.Concurrency.Workers))

	// 监控器已移除，使用日志系统
	// 如果需要metrics，可以后续添加

	// 创建扫描器
	scan, err := scanner.NewScanner(cfg, logger)
	if err != nil {
		return fmt.Errorf("create scanner: %w", err)
	}

	// 设置信号处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 启动扫描
	errChan := make(chan error, 1)
	go func() {
		errChan <- scan.Start()
	}()

	// 等待完成或中断
	select {
	case err := <-errChan:
		if err != nil {
			logger.Error("Scan failed", zap.Error(err))
			return err
		}
		logger.Info("Scan completed successfully")
	case sig := <-sigChan:
		logger.Info("Received signal, stopping scan", zap.String("signal", sig.String()))
		scan.Stop()
	}

	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}