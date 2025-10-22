#!/bin/bash
# 清理 domains.txt 文件
# 过滤IP地址和黑名单关键词

INPUT="domains/domains.txt.backup"
OUTPUT="domains/domains.txt.clean"

echo "开始清理 domains.txt..."
echo "原始文件: $INPUT"
echo "输出文件: $OUTPUT"

# 黑名单关键词（正则表达式，不区分大小写）
BLACKLIST_PATTERN="vip|casino|casinos|bet|betting|bets|gamble|gambling|gambler|poker|lottery|lotto|slot|slots|jackpot|bingo|roulette|blackjack|dice|wager|wagering|sportsbook|bookmaker|casin0|g4mble|b3t"

# IPv4 地址正则表达式
IPV4_PATTERN="https?://[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}"

# IPv6 地址正则表达式（简化版）
IPV6_PATTERN="https?://\[[:0-9a-fA-F]+\]"

echo "统计原始数据..."
TOTAL=$(wc -l < "$INPUT")
echo "总行数: $TOTAL"

echo "过滤中..."

# 使用 grep 过滤
# -v: 反向匹配（排除）
# -i: 不区分大小写
# -E: 扩展正则表达式

cat "$INPUT" | \
  grep -viE "$BLACKLIST_PATTERN" | \
  grep -vE "$IPV4_PATTERN" | \
  grep -vE "$IPV6_PATTERN" \
  > "$OUTPUT"

CLEAN=$(wc -l < "$OUTPUT")
REMOVED=$((TOTAL - CLEAN))

echo ""
echo "清理完成！"
echo "原始数据: $TOTAL 条"
echo "清理后: $CLEAN 条"
echo "移除: $REMOVED 条 ($(echo "scale=2; $REMOVED * 100 / $TOTAL" | bc)%)"
echo ""
echo "输出文件: $OUTPUT"
