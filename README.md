# WordPress REST API Scanner for Common Crawl

高性能的 WordPress REST API 扫描器，用于从 Common Crawl 数据集中发现包含 WordPress REST API 的网站。

## 特性

- 🚀 **高性能流式处理**：直接从 Common Crawl 流式读取 WAT 文件，无需下载到本地
- 🔄 **并发处理**：支持可配置的并发工作者数量
- 📊 **实时监控**：提供实时指标和进度显示
- 💾 **断点续扫**：支持从中断点继续扫描
- 🗜️ **压缩输出**：自动压缩输出为 NDJSON.gz 格式
- 🎯 **智能检测**：多模式检测 WordPress REST API 标识
- ⚙️ **高度可配置**：通过 YAML 配置文件和命令行参数灵活配置

## 安装

```bash
# 克隆项目
git clone https://github.com/yanyun/wp_crawl.git
cd wp_crawl

# 下载依赖
go mod download

# 编译
go build -o wp_crawl cmd/scanner/main.go
```

## 快速开始

### 基本扫描

```bash
# 扫描最新的 Common Crawl 批次
./wp_crawl scan --crawl CC-MAIN-2025-38

# 使用配置文件
./wp_crawl scan -c configs/config.yaml
```

### 高级用法

```bash
# 指定并发数和起始位置
./wp_crawl scan --crawl CC-MAIN-2025-38 --workers 20 --start-from 100

# 扫描特定段
./wp_crawl scan --crawl CC-MAIN-2025-38 --segments segment1,segment2

# 启用 CDX 验证
./wp_crawl scan --crawl CC-MAIN-2025-38 --cdx

# 禁用断点续扫
./wp_crawl scan --crawl CC-MAIN-2025-38 --no-resume

# 自定义检测模式
./wp_crawl scan --crawl CC-MAIN-2025-38 --patterns "api.w.org,wp-json"
```

## 配置说明

配置文件采用 YAML 格式，主要配置项：

```yaml
crawl:
  name: "CC-MAIN-2025-38"    # Common Crawl 批次 ID
  segments: []                # 要处理的段（空则处理所有）
  start_from: 0              # 起始 WAT 文件索引

concurrency:
  workers: 10                # 并发工作者数量
  download_queue: 50         # 下载队列大小
  process_queue: 100         # 处理队列大小

detection:
  patterns:                  # WordPress API 检测模式
    - "api.w.org"
    - "wp/v2"
    - "wp-json"
  enable_cdx: false          # 是否启用 CDX 验证
  pre_filter: true          # 预过滤优化

output:
  directory: "."            # 输出目录
  file_prefix: "hits"       # 输出文件前缀
  compress: true            # 启用 gzip 压缩
```

## 输出格式

输出为 NDJSON.gz 格式，每行一个 JSON 对象：

```json
{
  "url": "https://example.com",
  "timestamp": "2025-01-01T12:00:00Z",
  "source": "html_link",
  "wat_location": "crawl-data/CC-MAIN-2025-38/segments/...",
  "segment": "1234567890.12",
  "crawl_id": "CC-MAIN-2025-38",
  "metadata": {
    "link_url": "https://example.com/wp-json/",
    "link_rel": "https://api.w.org/"
  }
}
```

## 监控

启用监控后，可通过 HTTP 端点查看实时指标：

- `http://localhost:8080/metrics` - JSON 格式的详细指标
- `http://localhost:8080/stats` - 统计摘要
- `http://localhost:8080/health` - 健康检查

## 性能优化建议

1. **并发数调整**：根据网络带宽和 CPU 核心数调整 workers
2. **内存管理**：通过 `performance.max_memory_mb` 限制内存使用
3. **预过滤**：启用 `detection.pre_filter` 提升性能
4. **缓冲区大小**：调整 `output.buffer_size` 平衡内存和 I/O

## 项目结构

```
wp_crawl/
├── cmd/scanner/          # 命令行入口
├── internal/
│   ├── config/          # 配置管理
│   ├── detector/        # WordPress API 检测
│   ├── processor/       # WAT 文件处理
│   ├── scanner/         # 扫描器核心
│   ├── storage/         # 结果存储
│   └── monitor/         # 监控和日志
├── configs/             # 配置文件
└── scripts/            # 辅助脚本
```

## 技术栈

- **语言**：Go 1.21+
- **WAT 解析**：github.com/slyrz/warc
- **命令行**：cobra/viper
- **日志**：zap
- **并发**：golang.org/x/sync

## 性能指标

在标准配置下（10 并发工作者）：

- 处理速度：~500-1000 records/sec
- 内存使用：< 500MB
- 网络带宽：~10-50 Mbps
- 检测准确率：> 99%

## 故障排除

### 内存不足
- 减少 workers 数量
- 降低 queue 大小
- 启用内存限制

### 网络超时
- 增加 `network.timeout`
- 增加 `network.retry_attempts`
- 检查网络连接

### 断点续扫失败
- 检查 state 文件权限
- 确保 `resume.enable` 为 true
- 手动删除损坏的 state 文件

## License

MIT License

## 作者

- Yan Yun

## 贡献

欢迎提交 Issue 和 Pull Request！