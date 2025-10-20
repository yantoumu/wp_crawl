#!/bin/bash
# 测试单个 WAT 文件处理

set -e

CRAWL_ID="${1:-CC-MAIN-2024-51}"

echo "=========================================="
echo "WordPress 扫描器 - 单文件测试"
echo "=========================================="
echo "批次: $CRAWL_ID"
echo ""

# 1. 获取第一个 WAT 文件 URL
echo "[1/4] 获取 WAT 文件列表..."
WAT_PATHS_URL="https://data.commoncrawl.org/crawl-data/$CRAWL_ID/wat.paths.gz"

echo "下载清单: $WAT_PATHS_URL"
FIRST_WAT=$(curl -s "$WAT_PATHS_URL" | gunzip | head -n 1)

if [ -z "$FIRST_WAT" ]; then
    echo "错误: 无法获取 WAT 文件列表"
    echo ""
    echo "可能的批次ID (最近5个):"
    curl -s "https://data.commoncrawl.org/" | grep -o 'CC-MAIN-[0-9]\{4\}-[0-9]\{2\}' | sort -u | tail -n 5
    exit 1
fi

FULL_WAT_URL="https://data.commoncrawl.org/$FIRST_WAT"

echo "✓ 找到第一个 WAT 文件"
echo "  路径: $FIRST_WAT"
echo ""

# 2. 获取文件大小
echo "[2/4] 检查文件大小..."
FILE_SIZE=$(curl -sI "$FULL_WAT_URL" | grep -i content-length | awk '{print $2}' | tr -d '\r')

if [ -n "$FILE_SIZE" ]; then
    SIZE_MB=$((FILE_SIZE / 1024 / 1024))
    echo "✓ 文件大小: ${SIZE_MB} MB"
else
    echo "警告: 无法获取文件大小"
fi
echo ""

# 3. 运行扫描（只处理这一个文件）
echo "[3/4] 开始扫描..."
echo "预计时间: 1-3 分钟"
echo ""

# 创建临时配置，只处理第一个文件
./wp_crawl scan \
    --crawl "$CRAWL_ID" \
    --workers 1 \
    --start-from 0

echo ""

# 4. 查看结果
echo "[4/4] 结果统计..."
HITS_FILE="hits_${CRAWL_ID}.ndjson.gz"

if [ -f "$HITS_FILE" ]; then
    TOTAL_HITS=$(zcat "$HITS_FILE" 2>/dev/null | wc -l | tr -d ' ')

    echo "=========================================="
    echo "测试完成！"
    echo "=========================================="
    echo "处理文件: 1 个 WAT 文件 (${SIZE_MB} MB)"
    echo "找到网站: $TOTAL_HITS 个"
    echo "结果文件: $HITS_FILE"
    echo ""

    if [ "$TOTAL_HITS" -gt 0 ]; then
        echo "找到的网站（前10个）："
        zcat "$HITS_FILE" | jq -r '.url' | head -10

        echo ""
        echo "提取唯一域名："
        zcat "$HITS_FILE" | jq -r '.url' | awk -F/ '{print $3}' | sort -u
    else
        echo "警告: 这个 WAT 文件中没有找到 WordPress 网站"
        echo "这是正常的，不是所有 WAT 文件都包含 WordPress 网站"
    fi

    echo ""
    echo "=========================================="
    echo "完整扫描命令:"
    echo "./run_scan.sh $CRAWL_ID 12"
    echo "=========================================="
else
    echo "错误: 未生成结果文件"
fi
