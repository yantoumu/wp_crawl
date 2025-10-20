#!/bin/bash
# Script to get WAT file list from Common Crawl

CRAWL_ID="${1:-CC-MAIN-2025-38}"
OUTPUT_FILE="${2:-wat_files.txt}"

echo "Fetching WAT file list for crawl: $CRAWL_ID"

# Download and decompress WAT paths
curl -s "https://data.commoncrawl.org/crawl-data/${CRAWL_ID}/wat.paths.gz" | \
    gunzip | \
    head -100 > "$OUTPUT_FILE"

echo "WAT file list saved to: $OUTPUT_FILE"
echo "Total files: $(wc -l < $OUTPUT_FILE)"