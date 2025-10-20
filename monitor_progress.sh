#!/bin/bash
# 实时监控扫描进度

CRAWL_ID="${1:-CC-MAIN-2024-51}"
HITS_FILE="hits_${CRAWL_ID}.ndjson.gz"
STATE_FILE="scanner_state_${CRAWL_ID}.json"

echo "监控扫描进度: $CRAWL_ID"
echo "按 Ctrl+C 退出监控（不影响扫描程序）"
echo ""

while true; do
    clear
    echo "=========================================="
    echo "WordPress REST API 扫描进度"
    echo "=========================================="
    date
    echo ""

    # 检查状态文件
    if [ -f "$STATE_FILE" ]; then
        echo "扫描状态："
        cat "$STATE_FILE" | jq '{
            crawl_id,
            processed_wats,
            total_wats,
            total_hits,
            进度: "\(.processed_wats)/\(.total_wats) (\((.processed_wats * 100 / .total_wats) | floor)%)"
        }'
    else
        echo "状态文件不存在，扫描可能尚未开始"
    fi

    echo ""

    # 检查结果文件
    if [ -f "$HITS_FILE" ]; then
        CURRENT_HITS=$(zcat "$HITS_FILE" | wc -l | tr -d ' ')
        echo "当前命中数: $CURRENT_HITS"

        echo ""
        echo "最新 5 个命中："
        zcat "$HITS_FILE" | tail -5 | jq -r '.url'
    else
        echo "结果文件尚未生成"
    fi

    echo ""
    echo "=========================================="
    echo "等待 10 秒后刷新..."

    sleep 10
done
