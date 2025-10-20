#!/bin/bash
# 检查 Common Crawl WAT 文件的实际大小

CRAWL_ID="${1:-CC-MAIN-2025-38}"
SAMPLE_COUNT="${2:-10}"

echo "正在获取 $CRAWL_ID 的 WAT 文件列表..."
echo ""

# 下载 wat.paths.gz
PATHS_URL="https://data.commoncrawl.org/crawl-data/$CRAWL_ID/wat.paths.gz"

echo "下载清单文件: $PATHS_URL"
WAT_LIST=$(curl -s "$PATHS_URL" | gunzip | head -n "$SAMPLE_COUNT")

if [ -z "$WAT_LIST" ]; then
    echo "错误: 无法获取 WAT 文件列表"
    echo "可能的原因:"
    echo "1. 批次 ID 不存在"
    echo "2. 网络问题"
    echo ""
    echo "尝试查看可用的批次:"
    curl -s "https://data.commoncrawl.org/" | grep -o 'CC-MAIN-[0-9]\{4\}-[0-9]\{2\}' | sort -u | tail -n 5
    exit 1
fi

echo ""
echo "========================================="
echo "检查前 $SAMPLE_COUNT 个 WAT 文件的大小"
echo "========================================="
echo ""

TOTAL_SIZE=0
COUNT=0

while IFS= read -r WAT_PATH; do
    [ -z "$WAT_PATH" ] && continue

    COUNT=$((COUNT + 1))
    FULL_URL="https://data.commoncrawl.org/$WAT_PATH"

    echo "[$COUNT/$SAMPLE_COUNT] 检查文件大小..."

    # 使用 HEAD 请求获取文件大小（不下载文件）
    SIZE=$(curl -sI "$FULL_URL" | grep -i content-length | awk '{print $2}' | tr -d '\r')

    if [ -n "$SIZE" ]; then
        SIZE_MB=$((SIZE / 1024 / 1024))
        TOTAL_SIZE=$((TOTAL_SIZE + SIZE))
        echo "  大小: $SIZE_MB MB"
        echo "  路径: $WAT_PATH"
    else
        echo "  无法获取大小"
    fi
    echo ""
done <<< "$WAT_LIST"

echo "========================================="
echo "统计摘要"
echo "========================================="

if [ "$COUNT" -gt 0 ] && [ "$TOTAL_SIZE" -gt 0 ]; then
    AVG_SIZE=$((TOTAL_SIZE / COUNT))
    AVG_SIZE_MB=$((AVG_SIZE / 1024 / 1024))
    TOTAL_SIZE_GB=$((TOTAL_SIZE / 1024 / 1024 / 1024))

    echo "采样文件数: $COUNT"
    echo "总大小: $TOTAL_SIZE_GB GB"
    echo "平均大小: $AVG_SIZE_MB MB/文件"
    echo ""

    # 估算完整批次大小
    echo "========================================="
    echo "完整批次估算"
    echo "========================================="

    # 获取总文件数
    TOTAL_FILES=$(curl -s "$PATHS_URL" | gunzip | wc -l | tr -d ' ')

    if [ -n "$TOTAL_FILES" ] && [ "$TOTAL_FILES" -gt 0 ]; then
        ESTIMATED_TOTAL=$((AVG_SIZE * TOTAL_FILES))
        ESTIMATED_TOTAL_GB=$((ESTIMATED_TOTAL / 1024 / 1024 / 1024))

        echo "WAT 文件总数: $TOTAL_FILES"
        echo "估算总大小: $ESTIMATED_TOTAL_GB GB"
        echo ""
        echo "如果下载所有文件到磁盘:"
        echo "  需要空间: $ESTIMATED_TOTAL_GB GB"
        echo ""
        echo "使用流式处理:"
        echo "  磁盘占用: 仅结果文件 (~50 MB)"
        echo "  内存占用: ~700 MB (12并发)"
        echo "  节省磁盘: $ESTIMATED_TOTAL_GB GB (99.99%)"
    fi
else
    echo "无法计算统计信息"
fi

echo ""
echo "========================================="
echo "提示"
echo "========================================="
echo "1. WAT 文件已 gzip 压缩，解压后约为压缩后的 5-8 倍"
echo "2. 流式处理无需下载完整文件到磁盘"
echo "3. 每个文件包含约 40,000-80,000 条网页元数据"
