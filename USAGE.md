# WordPress REST API Scanner - 使用指南

这是一个高性能的 Common Crawl WordPress REST API 扫描器，用于发现包含 WordPress REST API 端点的网站。

## 快速开始

### 1. 基本扫描

扫描 Common Crawl 的特定批次（例如 CC-MAIN-2025-38）：

```bash
./wp_crawl scan --crawl CC-MAIN-2025-38
```

### 2. 小规模测试

先从少量文件开始测试（推荐第一次使用）：

```bash
# 只处理前50个 WAT 文件
./wp_crawl scan --crawl CC-MAIN-2025-38 --start-from 0 --workers 5
```

注意：程序会自动从 `wat.paths.gz` 获取文件列表，但由于原始文件可能很大，建议先小规模测试。

### 3. 调整并发数

根据网络带宽和机器性能调整工作线程数：

```bash
# 使用16个并发worker
./wp_crawl scan --crawl CC-MAIN-2025-38 --workers 16
```

### 4. 自定义检测模式

默认检测 `api.w.org`、`wp/v2`、`wp-json`，可以自定义：

```bash
./wp_crawl scan --crawl CC-MAIN-2025-38 \
  --patterns "api.w.org,wp-json,wordpress"
```

### 5. 断点续扫

程序默认启用断点续扫功能，中断后重新运行会自动继续：

```bash
# 第一次运行（可能被中断）
./wp_crawl scan --crawl CC-MAIN-2025-38

# 自动从上次中断的地方继续
./wp_crawl scan --crawl CC-MAIN-2025-38
```

如果要禁用断点续扫：

```bash
./wp_crawl scan --crawl CC-MAIN-2025-38 --no-resume
```

### 6. 日志和监控

启用详细日志：

```bash
./wp_crawl scan --crawl CC-MAIN-2025-38 \
  --log-level debug \
  --log-file scanner.log
```

禁用进度条（适合在脚本中运行）：

```bash
./wp_crawl scan --crawl CC-MAIN-2025-38 --no-progress
```

## 输出结果

### 结果文件

程序会在当前目录生成结果文件：

- `hits_CC-MAIN-2025-38.ndjson.gz` - 压缩的 NDJSON 格式命中结果
- `scanner_state_CC-MAIN-2025-38.json` - 扫描状态文件（用于断点续扫）

### 结果格式

每条命中记录的格式为：

```json
{
  "url": "https://example.com/",
  "timestamp": "2025-10-20T10:30:00Z",
  "source": "html_link",
  "wat_location": "https://data.commoncrawl.org/crawl-data/CC-MAIN-2025-38/segments/.../wat/...",
  "segment": "1234567890.123",
  "crawl_id": "CC-MAIN-2025-38",
  "metadata": {
    "link_url": "https://example.com/wp-json/",
    "link_rel": "https://api.w.org/"
  }
}
```

### 查看结果

解压并查看结果：

```bash
# 查看前10条结果
zcat hits_CC-MAIN-2025-38.ndjson.gz | head -10

# 统计总命中数
zcat hits_CC-MAIN-2025-38.ndjson.gz | wc -l

# 提取所有URL
zcat hits_CC-MAIN-2025-38.ndjson.gz | jq -r '.url' > wordpress_sites.txt
```

## 高级功能

### 处理特定段（Segments）

只处理特定的数据段：

```bash
./wp_crawl scan --crawl CC-MAIN-2025-38 \
  --segments "1234567890.123,1234567890.456"
```

### 配置文件

创建配置文件 `config.yaml`：

```yaml
crawl:
  name: CC-MAIN-2025-38
  start_from: 0

concurrency:
  workers: 12
  download_queue: 100

detection:
  patterns:
    - api.w.org
    - wp/v2
    - wp-json
  pre_filter: true

network:
  timeout: 180s
  retry_attempts: 3
  retry_delay: 1s
  user_agent: "wp_crawl/1.0"

output:
  format: ndjson
  compression: gzip

resume:
  enable: true
  state_file: scanner_state.json
  save_interval: 60s

monitor:
  enable: true
  progress_bar: true
  metrics_port: 8080
```

使用配置文件：

```bash
./wp_crawl scan -c config.yaml
```

### 性能监控

程序会在端口 8080 提供监控指标（如果启用）：

```bash
# 查看实时指标
curl http://localhost:8080/metrics
```

## 常见问题

### Q: 程序运行很慢？

A: 调整以下参数：
- 增加并发数：`--workers 20`
- 检查网络带宽
- 使用更快的磁盘（SSD）

### Q: 如何估算运行时间？

A: 一个 WAT 文件大约包含数万到数十万条记录，处理时间取决于：
- 网络速度（下载 WAT 文件）
- CPU 性能（JSON 解析）
- 并发数

典型速率：每个 worker 每分钟处理 1-3 个 WAT 文件。

### Q: 磁盘空间不足？

A: 程序设计为流式处理，不会保存 WAT 文件到磁盘。只有命中结果会被保存：
- 每万条命中约 5-10 MB（压缩后）
- 检查 `hits_*.ndjson.gz` 文件大小
- 定期清理旧的扫描结果

### Q: 如何处理多个批次？

A: 依次扫描不同批次：

```bash
./wp_crawl scan --crawl CC-MAIN-2025-38
./wp_crawl scan --crawl CC-MAIN-2025-37
./wp_crawl scan --crawl CC-MAIN-2025-36
```

或使用脚本批量处理：

```bash
for crawl in CC-MAIN-2025-{38..30}; do
  ./wp_crawl scan --crawl $crawl
done
```

## 性能优化建议

1. **网络优化**
   - 使用高带宽网络
   - 考虑在云服务器上运行（靠近 Common Crawl 的 S3）
   - 调整 `network.timeout` 和 `network.retry_attempts`

2. **并发优化**
   - CPU 密集型：workers = CPU 核心数
   - I/O 密集型：workers = CPU 核心数 × 2-4
   - 监控系统负载，避免过载

3. **内存优化**
   - 程序使用流式处理，内存占用稳定
   - 典型内存使用：< 500 MB
   - 如果内存不足，减少 `concurrency.download_queue`

4. **磁盘优化**
   - 使用 SSD
   - 确保有足够空间存储结果
   - 定期清理旧文件

## 故障排除

### 查看日志

```bash
./wp_crawl scan --crawl CC-MAIN-2025-38 \
  --log-level debug \
  --log-file debug.log
```

### 验证配置

```bash
./wp_crawl validate
```

### 查看版本

```bash
./wp_crawl version
```

## 技术细节

- **流式处理**: WAT 文件从 Common Crawl 直接流式下载和解析，不落盘
- **内存优化**: 使用子串预筛选 + JSON 解析的两阶段检测，大幅减少 JSON 解析次数
- **并发控制**: Worker pool 模式，支持可配置的并发数
- **容错机制**: HTTP 自动重试、分片级容错、断点续扫
- **输出格式**: NDJSON (换行分隔JSON) + gzip 压缩

## 获取帮助

```bash
# 查看所有命令
./wp_crawl --help

# 查看特定命令帮助
./wp_crawl scan --help
./wp_crawl validate --help
```
