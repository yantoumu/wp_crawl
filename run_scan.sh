#!/bin/bash
# WordPress REST API 完整扫描脚本
# 自动完成：扫描 → 提取 URL → 去重

set -e  # 遇到错误立即退出

CRAWL_ID="${1:-CC-MAIN-2024-51}"
WORKERS="${2:-12}"

echo "=========================================="
echo "WordPress REST API 扫描程序"
echo "=========================================="
echo "批次ID: $CRAWL_ID"
echo "并发数: $WORKERS"
echo "预计时间: ~5-7 天（100,000 个文件）"
echo "=========================================="
echo ""

# 1. 运行扫描程序
echo "[1/3] 开始扫描 WAT 文件..."
./wp_crawl scan --crawl "$CRAWL_ID" --workers "$WORKERS"

if [ $? -ne 0 ]; then
    echo "错误: 扫描失败"
    exit 1
fi

echo ""
echo "[2/3] 提取 URL..."

# 2. 提取所有 URL
HITS_FILE="hits_${CRAWL_ID}.ndjson.gz"
URLS_FILE="urls_${CRAWL_ID}.txt"
UNIQUE_URLS_FILE="unique_urls_${CRAWL_ID}.txt"

if [ ! -f "$HITS_FILE" ]; then
    echo "错误: 结果文件不存在: $HITS_FILE"
    exit 1
fi

# 提取 URL（使用 jq）
zcat "$HITS_FILE" | jq -r '.url' > "$URLS_FILE"

echo ""
echo "[3/3] 去重..."

# 3. 去重
sort -u "$URLS_FILE" > "$UNIQUE_URLS_FILE"

# 统计
TOTAL_HITS=$(wc -l < "$URLS_FILE" | tr -d ' ')
UNIQUE_URLS=$(wc -l < "$UNIQUE_URLS_FILE" | tr -d ' ')

echo ""
echo "=========================================="
echo "扫描完成！"
echo "=========================================="
echo "总命中数: $TOTAL_HITS"
echo "去重后:   $UNIQUE_URLS"
echo "重复率:   $(echo "scale=2; ($TOTAL_HITS - $UNIQUE_URLS) * 100 / $TOTAL_HITS" | bc)%"
echo ""
echo "结果文件:"
echo "  - 原始数据: $HITS_FILE"
echo "  - 所有URL:  $URLS_FILE"
echo "  - 去重URL:  $UNIQUE_URLS_FILE  ⭐"
echo "=========================================="

# 显示前 10 个结果
echo ""
echo "前 10 个 URL："
head -10 "$UNIQUE_URLS_FILE"

# 清理临时文件（可选）
# rm -f "$URLS_FILE"
