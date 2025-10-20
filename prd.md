阿飞，既然验证过思路可行，下面给你一份Go 开发文档（能直接落地的实现规划 + 关键代码片段），把需求、流程、实现细节、观测与扩展都讲透。你 1TB 硬盘的约束会被优先满足：全程流式处理 WAT，只落命中结果与最小化统计。

⸻

目标 & 背景

目标：在 Common Crawl 的某一批次（先从 CC-MAIN-2025-38 开始）里，找出包含 api.w.org 的网页（HTML 里 <link rel="https://api.w.org/"> 或 HTTP Link 响应头），输出 URL、抓取时间、命中来源与分片信息，用于后续建站/变现。
理由：api.w.org 是 WordPress REST API 的发现链接，通常同时存在于 Link 响应头和 HTML <link> 中。

数据与接口（权威规则）
	•	每个批次目录（如 crawl-data/CC-MAIN-2025-XX/）包含三份清单：warc.paths.gz / wat.paths.gz / wet.paths.gz。给每一行加前缀 https://data.commoncrawl.org/ 就得到可下载直链。 ￼
	•	WAT 是 WARC 容器中的JSON 元数据（解析后的 HTTP 响应头、从 HTML 提取的链接等），正好能命中 api.w.org。
	•	需要回源复核时，用 Index Server (CDX) 查 filename/offset/length，再对 data.commoncrawl.org/<WARC> 做 HTTP Range 获取原始记录。 ￼

⸻

一、需求说明（PRD 风格）

用户是谁？
独立开发者/数据挖掘者，目标是批量发现带 WordPress REST API 的站点，做工具与内容聚合（SEO/订阅变现）。

必须满足
	1.	低磁盘占用（1TB 约束）：不落盘 WAT 分片；仅保存命中结果（NDJSON.gz）。
	2.	稳：网络波动可重试、分片级容错、断点续扫。
	3.	准：只认两处来源：HTML-Metadata → Links 与 HTTP-Response-Metadata → Headers.Link。
	4.	快：可配置并发、I/O 流式、先子串预筛再 JSON 解析。
	5.	可核验：支持抽样用 CDX → Range 拉原始 HTML/头验证。
	6.	可扩展：关键词可配；后续支持多批次、增量跑、域名去重、导入 OLAP/搜索引擎。

非功能
	•	只做发现与导出，不做全量 HTML 抓取与复杂解析（那是下游任务）。
	•	不依赖大数据集群，单机 Go 即可。

⸻

二、整体架构

Pipeline（单机并发）
	1.	ListFetcher：下载 wat.paths.gz → 解析出所有 WAT 相对路径 → 拼 HTTP 直链。
	2.	WAT Worker Pool（N 并发）：
	•	HTTP 流式 GET（不落盘），读 .wat.gz；
	•	按 WARC 记录迭代读取 → 取 metadata 记录里的 JSON；
	•	先做字节级子串预筛（"api.w.org"），命中再 encoding/json 解析；
	•	只输出命中行（URL、日期、来源、所在 WAT URL）。
	3.	Sink：结果按行写入 hits_<crawl>.ndjson.gz；可选同步域名聚合 domains_<crawl>_top.txt。
	4.	Verifier（可选）：对样本 URL 调 CDX，拿 filename/offset/length 后做 Range 回源，存 HTML 片段用于 QA。 ￼
	5.	Metrics/Logs：每分片耗时、吞吐、错误率；中途断点文件记录已完成分片。

技术选型（Golang）
	•	WARC/WAT 读取：推荐 github.com/slyrz/warc（轻量，支持 gzip 流），或 github.com/nlnwa/gowarc / github.com/internetarchive/gowarc（更强大）。本文示例用 slyrz/warc。 ￼
	•	HTTP：net/http（自定义 Transport、超时、重试/回退）。
	•	压缩：compress/gzip 写结果；可选 klauspost/compress/zstd。
	•	并发：标准 errgroup/ worker 池。
	•	JSON：encoding/json（够用）。
	•	CLI：标准库 flag 或 cobra。

⸻

三、数据模型与输出

命中结果 (NDJSON)

{
  "url": "https://example.com/",
  "warc_date": "2025-09-09T12:34:56Z",
  "hit": ["html_links", "link_header"],
  "wat": "https://data.commoncrawl.org/crawl-data/CC-MAIN-2025-38/segments/.../wat/CC-MAIN-...wat.gz",
  "crawl": "CC-MAIN-2025-38"
}

命中判定路径（WAT JSON 模式的关键字段）
Envelope → WARC-Header-Metadata → WARC-Type == "response"
Envelope → Payload-Metadata → HTTP-Response-Metadata → HTML-Metadata → Links[]（看 .url）
Envelope → Payload-Metadata → HTTP-Response-Metadata → Headers → Link（数组或字符串）
官方“Get Started”已给出 WAT 结构骨架与解释，明确包含 Headers 和 HTML-Metadata.Links。

⸻

四、流程（一步步）

1) 拉取清单（wat.paths.gz）
	•	GET https://data.commoncrawl.org/crawl-data/CC-MAIN-2025-38/wat.paths.gz
	•	gunzip 后逐行读取；每行前缀加 https://data.commoncrawl.org/ 得直链。

2) 并发流式处理每个 WAT
	•	HTTP GET（Do(req)，resp.Body 直接喂给 WARC Reader；warc.NewReader 可处理 gzip）。
	•	逐条 ReadRecord()：只处理 metadata 记录（其 Content 是 WAT 的 JSON）。
	•	先子串预筛：bytes.Contains(payload, []byte("api.w.org"))，没命中就跳过，命中再 JSON 解析。
	•	JSON 解析后：
	•	过滤 Envelope.WARC-Header-Metadata.WARC-Type=="response"；
	•	检查 HTML-Metadata.Links[*].url 是否含 api.w.org；
	•	汇总 Headers.Link（list→字符串）是否含 api.w.org。
	•	写命中 NDJSON（Gzip Writer 流式）。

**为何用 WAT 而不是 WARC 全量？**WAT 已经帮你“解析出链接与响应头”，体积小、扫描更快；且官方明确 WAT 为此用途设计。

3) 可选核验（CDX → Range）
	•	调 Index Server：
http://index.commoncrawl.org/CC-MAIN-2025-38-index?url=<encoded>&output=json&limit=1&filter=status:200&filter=mime:text/html
	•	取到 filename/offset/length 后，对 https://data.commoncrawl.org/<filename> 发 Range: bytes=offset-...，得到单条 WARC 记录（含原始 HTML/头），确认确实有 api.w.org。 ￼

⸻

五、关键代码（最小可跑骨架）

说明：以下片段展示核心读法与命中逻辑；实际项目建议拆分为 list.go / worker.go / cdx.go / sink.go 等。

// go.mod 里：
// require (
//   github.com/slyrz/warc v0.0.0-... // 读取 WARC/WAT
// )

package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/slyrz/warc"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	crawlID   = "CC-MAIN-2025-38"
	listURL   = "https://data.commoncrawl.org/crawl-data/" + crawlID + "/wat.paths.gz"
	baseURL   = "https://data.commoncrawl.org/"
	userAgent = "cc-apiworg-go/1.0"
)

type Hit struct {
	URL      string   `json:"url"`
	WarcDate string   `json:"warc_date"`
	Hit      []string `json:"hit"`
	WAT      string   `json:"wat"`
	Crawl    string   `json:"crawl"`
}

func httpClient() *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     60 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: 180 * time.Second}
}

func fetchWATPaths(ctx context.Context, cli *http.Client, limit int) ([]string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}
	gr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	paths := []string{}
	sc := bufio.NewScanner(gr)
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		if p == "" {
			continue
		}
		paths = append(paths, baseURL+p)
		if limit > 0 && len(paths) >= limit {
			break
		}
	}
	return paths, sc.Err()
}

func processOneWAT(ctx context.Context, cli *http.Client, watURL string, hits chan<- Hit) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", watURL, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("bad status %s for %s", resp.Status, watURL)
	}
	// 直接把响应体交给 warc.Reader（其自带 gzip 支持）
	rdr, err := warc.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer rdr.Close()

	for {
		rec, err := rdr.ReadRecord()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		// 只要 metadata 记录（WAT JSON 放在这）
		if strings.ToLower(rec.Header.Get("warc-type")) != "metadata" {
			continue
		}
		buf, err := io.ReadAll(rec.Content)
		if err != nil {
			continue
		}
		// 轻量预筛：没出现过 api.w.org 直接跳过
		if !strings.Contains(string(buf), "api.w.org") {
			continue
		}
		// 解析 JSON → 按 WAT 结构取字段
		var j map[string]any
		if err := json.Unmarshal(buf, &j); err != nil {
			continue
		}
		env, _ := j["Envelope"].(map[string]any)
		hdr, _ := env["WARC-Header-Metadata"].(map[string]any)
		if fmt.Sprint(hdr["WARC-Type"]) != "response" {
			continue
		}
		url := fmt.Sprint(hdr["WARC-Target-URI"])
		date := fmt.Sprint(hdr["WARC-Date"])

		pm, _ := env["Payload-Metadata"].(map[string]any)
		httpm, _ := pm["HTTP-Response-Metadata"].(map[string]any)

		// HTML-Metadata → Links
		hitSources := []string{}
		if htmlm, ok := httpm["HTML-Metadata"].(map[string]any); ok {
			if links, ok := htmlm["Links"].([]any); ok {
				for _, it := range links {
					if m, ok := it.(map[string]any); ok {
						if strings.Contains(fmt.Sprint(m["url"]), "api.w.org") {
							hitSources = append(hitSources, "html_links")
							break
						}
					}
				}
			}
		}
		// Headers → Link
		if hdrs, ok := httpm["Headers"].(map[string]any); ok {
			if l, ok := hdrs["Link"]; ok {
				switch v := l.(type) {
				case []any:
					joined := []string{}
					for _, s := range v {
						joined = append(joined, fmt.Sprint(s))
					}
					if strings.Contains(strings.Join(joined, " "), "api.w.org") {
						hitSources = append(hitSources, "link_header")
					}
				default:
					if strings.Contains(fmt.Sprint(v), "api.w.org") {
						hitSources = append(hitSources, "link_header")
					}
				}
			}
		}
		if len(hitSources) > 0 && url != "" {
			hits <- Hit{URL: url, WarcDate: date, Hit: uniq(hitSources), WAT: watURL, Crawl: crawlID}
		}
	}
}

func uniq(ss []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func main() {
	ctx := context.Background()
	cli := httpClient()

	// 先小样：只跑 50 个分片；全量时改成 0（不限制）
	wats, err := fetchWATPaths(ctx, cli, 50)
	if err != nil {
		panic(err)
	}
	fmt.Println("[info]", "WAT files:", len(wats))

	// 结果 gzip 写出
	outf, _ := os.Create("hits_" + crawlID + ".ndjson.gz")
	defer outf.Close()
	zw := gzip.NewWriter(outf)
	defer zw.Close()

	hits := make(chan Hit, 1024)
	var wg sync.WaitGroup

	// sink
	wg.Add(1)
	go func() {
		defer wg.Done()
		enc := json.NewEncoder(zw)
		for h := range hits {
			_ = enc.Encode(h)
		}
	}()

	// 简单 worker 池
	concurrency := 12
	sem := make(chan struct{}, concurrency)
	for _, u := range wats {
		sem <- struct{}{}
		go func(url string) {
			defer func() { <-sem }()
			if err := processOneWAT(ctx, cli, url, hits); err != nil {
				fmt.Fprintln(os.Stderr, "[warn]", err)
			}
		}(u)
	}
	// drain
	for i := 0; i < cap(sem); i++ {
		sem <- struct{}{}
	}
	close(hits)
	wg.Wait()
	fmt.Println("[done]")
}

上面用到的 WARC 读法与示例 API，来自 github.com/slyrz/warc 的官方文档；它支持 gzip/bzip2 压缩并提供 ReadRecord() 逐条读取。

⸻

六、性能与资源规划（1TB 友好）
	•	磁盘：不落盘分片，只写命中结果（gz 压缩），一般几十 MB～数百 MB量级。
	•	带宽 & 并发：默认 8–16 个分片并发较稳；遇到出口受限就降到 8。
	•	JSON 解析开销：先子串预筛再 JSON，能显著减少解析次数。
	•	恢复：记录“已完成 WAT URL”到本地状态文件，崩溃重启从未处理列表继续。
	•	WAT 结构演进：WAT 会按需扩展字段（例如语言属性的新增），但 Headers.Link 与 HTML-Metadata.Links 的语义稳定。 ￼

⸻

七、CDX 回源核验（可选但推荐）

查询

GET http://index.commoncrawl.org/CC-MAIN-2025-38-index
  ?url=<url-encoded>
  &output=json
  &filter=status:200
  &filter=mime:text/html
  &limit=1

Index Server 是基于 CDX 的查询 API；Common Crawl 官方页面与公告都有说明与示例。 ￼

取原文
从返回中抠出 filename/offset/length，对 https://data.commoncrawl.org/<filename> 发 Range 请求即可得到单条 WARC 记录内容用于比对。（流程见官方 Get Started 与社区教程） ￼

⸻

八、命令行与配置建议
	•	--crawl-id：默认 CC-MAIN-2025-38
	•	--max-files：默认 0（全量）；小样时设 50/200
	•	--concurrency：默认 12
	•	--keyword：默认 api.w.org（可扩展为多关键字）
	•	--out：默认 hits_<crawl>.ndjson.gz
	•	--verify-sample：抽样 N 条做 CDX 回源
	•	--resume：从状态文件继续
	•	--timeout, --retries, --backoff

下载直链生成规则与示例命令，官方已在 “Get Started / Latest Crawl” 明确给出。 ￼

⸻

九、观测与质量
	•	日志：分片级开始/结束、速率、错误（超时/EOF/JSON 失败）。
	•	指标：每分片命中率、总命中数、URL→域名聚合分布（Top N）。
	•	QA：每 1,000 命中抽 20 条用 CDX 回源核验（误报<1% 作为门槛）。

⸻

十、测试计划
	1.	单测：对解析函数投喂带/不带 api.w.org 的 WAT JSON 片段，验证判定逻辑。
	2.	集成测试：下载 1 个 WAT 分片，跑完整流程并校验输出至少有 X 条命中（zgrep 对照）。
	3.	回归：更换批次 ID 后能正常跑通（验证清单拼接稳定）。 ￼

⸻

十一、扩展路线（按需）
	•	用 Athena/Parquet 索引做候选 URL 预筛（少传输），再回源 Range 抓取——官方提供了 cc-index-table 与示例 SQL。 ￼
	•	多关键词：例如同时挖 oembed、wp-json 等站点特征。
	•	落库：写 ClickHouse/SQLite + Web UI（域名画像、地域/语言分布）。
	•	去重：域名级去重 + 首条样例 URL 保留。
	•	WARC 验证工具链：Internet Archive 的 gowarc 自带 CLI（extract/verify/mend）。

⸻

立即行动清单（TL;DR）
	1.	按上面 Go 骨架新建项目，先把 limit=50 跑一轮，确认命中与输出格式。
	2.	开 --verify-sample=50 做 CDX 抽样核验，确认误报低。 ￼
	3.	放开 --max-files=0 跑全量当月；持续输出域名 Top。
	4.	稳定后加「多批次串行」与「Athena 预筛」两条加速线。 ￼

⸻

参考 / 佐证
	•	Get Started：下载前缀、WARC/WAT/WET 说明、WAT JSON 骨架与字段（含 Headers/HTML-Metadata.Links）。
	•	WAT/WARC 格式解读：WAT 含响应头与从 HTML 提取的 Links 元数据。 ￼
	•	Index Server (CDX)：官方公告与入口页。 ￼
	•	Go 读取 WARC：slyrz/warc（Reader 示例），nlnwa/gowarc，internetarchive/gowarc。 ￼
	•	WordPress REST 发现：Link 头与 <link rel="https://api.w.org/">。

如果你要，我可以把这套骨架封装成可执行 CLI（带 --crawl-id/--max-files/--concurrency/--verify-sample 等参数），再给你一份 Makefile 与 Dockerfile，直接 go build && ./cc-scan 起飞。