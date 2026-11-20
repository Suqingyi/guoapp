package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 番果（fanguo）原生站源：MacCMS 风格 JSON 采集接口，番剧/国创/欧美动漫。
//
// 上游接口（实测 2026-11）：
//   GET {base}?ac=list                      分类 class[]
//   GET {base}?ac=videolist&t={id}&pg={n}   分类目录
//   GET {base}?ac=videolist&wd={kw}&pg={n}  在线搜索
//   GET {base}?ac=videolist&ids={id}        详情（vod_play_from / vod_play_url）
//   GET {playURL}/index.m3u8                分集播放页对应的 M3U8 直链
//
// base 为 {mirror}/api.php/provide/vod。镜像按顺序尝试并记住有效域名。

const (
	fanguoMaxPage    = 100000
	fanguoMaxID      = 64
	fanguoMaxKeyword = 256
	fanguoPageSize   = 30
)

// fanguoMirror 是一个采集镜像：基础地址 + 规范分类到该镜像原生 t 的映射。
// 三个镜像实测（2026-11）：极速与红牛同源，樱花为独立源且日番最全。
type fanguoMirror struct {
	base       string
	categories map[string]string
}

// 顺序即优先级：红牛的播放列表为单一域名的连续流（无 EXT-X-DISCONTINUITY，片头不插广告）；
// 樱花在片头注入独立广告分片，会使正片整体延后、压制字幕时间轴错位；
// 极速的采集接口可达但视频 CDN 已拒绝连接，命中后会报播放列表读取失败。
var fanguoMirrors = []fanguoMirror{
	{"https://www.hongniuzy2.com/api.php/provide/vod", map[string]string{"cn": "36", "jp": "37", "us": "38"}},
	{"https://yhzy.cc/api.php/provide/vod", map[string]string{"cn": "30", "jp": "31", "us": "32"}},
	{"https://jszyapi.com/api.php/provide/vod", map[string]string{"cn": "24", "jp": "25", "us": "26"}},
}

// fanguoCategoryNames 是对外展示的规范分类，顺序即界面顺序。
var fanguoCategoryNames = []nativeCategory{
	{ID: "jp", Name: "日本动漫"},
	{ID: "cn", Name: "国产动漫"},
	{ID: "us", Name: "欧美动漫"},
}

var (
	reFanguoTag     = regexp.MustCompile(`<[^>]*>`)
	reFanguoM3U8URL = regexp.MustCompile(`(?i)https?://[^\s"'<>\\]+\.m3u8[^\s"'<>\\]*`)
	reFanguoEpisode = regexp.MustCompile(`(\d+(?:\.\d+)?)`)
)

func fanguoNumericID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > fanguoMaxID {
		return false
	}
	number, err := strconv.Atoi(value)
	return err == nil && number > 0
}

func validFanguoCategory(category string) bool {
	if category == "" {
		return true
	}
	for _, entry := range fanguoCategoryNames {
		if entry.ID == category {
			return true
		}
	}
	number, err := strconv.Atoi(category)
	return err == nil && number > 0 && number <= 100000
}

// leadingYear 从日期文本开头提取四位年份。
func leadingYear(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 4 {
		return ""
	}
	digits := value[:4]
	for _, r := range digits {
		if r < '0' || r > '9' {
			return ""
		}
	}
	year, err := strconv.Atoi(digits)
	if err != nil || year < 1900 || year > 2200 {
		return ""
	}
	return digits
}

// fanguoTitleKey 归一标题用于同页去重：去空白、括号与季部篇后缀差异。
func fanguoTitleKey(title string) string {
	title = strings.ToLower(fanguoStripTags(title))
	var builder strings.Builder
	for _, r := range title {
		switch r {
		case ' ', '\t', '（', '）', '(', ')', '【', '】', '[', ']', '·', '-', '_', '第', '季', '部', '篇', '：', ':', ',', '，':
			continue
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func fanguoStripTags(value string) string {
	return strings.TrimSpace(reFanguoTag.ReplaceAllString(value, ""))
}

func fanguoPlayURL(raw string) string {
	if isProviderHTTPMediaURL(raw) {
		return raw
	}
	return ""
}

func (d *Downloader) fanguoGet(ctx context.Context, category string, queryFor func(mirror fanguoMirror) url.Values, limit int64) (any, error) {
	if limit <= 0 {
		limit = 16 << 20
	}
	timeout := 15 * time.Second
	if background, _ := ctx.Value(backgroundCatalogKey{}).(bool); background {
		timeout = 8 * time.Second
	}
	mirrors := d.fanguoMirrorCandidates(category)
	var lastErr error
	tried := 0
	for _, mirror := range mirrors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tried++
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		value, err := d.fanguoGetOnce(attemptCtx, mirror.base, queryFor(mirror), limit)
		cancel()
		if err == nil {
			if parsed, parseErr := url.Parse(mirror.base); parseErr == nil {
				d.providerMu.Lock()
				d.providerHosts[sourceFanguo] = parsed.Scheme + "://" + parsed.Host
				d.providerMu.Unlock()
			}
			return value, nil
		}
		var backoff *requestBackoff
		if errors.As(err, &backoff) {
			return nil, err
		}
		lastErr = err
		if apiErr, ok := err.(*fanguoAPIError); ok && apiErr.permanent {
			break
		}
	}
	if tried > 1 && lastErr != nil {
		return nil, fmt.Errorf("番果来源请求失败：已尝试 %d 个域名，最后错误：%w", tried, publicError(lastErr))
	}
	return nil, lastErr
}

// fanguoMirrorCandidates 把记住的有效镜像排最前；分类在该镜像无映射时跳过它。
func (d *Downloader) fanguoMirrorCandidates(category string) []fanguoMirror {
	d.providerMu.Lock()
	preferred := d.providerHosts[sourceFanguo]
	d.providerMu.Unlock()
	preferredHost := ""
	if parsed, err := url.Parse(preferred); err == nil {
		preferredHost = strings.ToLower(parsed.Host)
	}
	mirrors := make([]fanguoMirror, 0, len(fanguoMirrors))
	if preferredHost != "" {
		for _, mirror := range fanguoMirrors {
			if base, err := url.Parse(mirror.base); err == nil && strings.EqualFold(base.Host, preferredHost) {
				mirrors = append(mirrors, mirror)
			}
		}
	}
	for _, mirror := range fanguoMirrors {
		already := false
		for _, entry := range mirrors {
			if entry.base == mirror.base {
				already = true
			}
		}
		if !already {
			mirrors = append(mirrors, mirror)
		}
	}
	if category != "" {
		usable := make([]fanguoMirror, 0, len(mirrors))
		for _, mirror := range mirrors {
			if _, ok := mirror.categories[category]; ok || !isCanonicalFanguoCategory(category) {
				usable = append(usable, mirror)
			}
		}
		if len(usable) > 0 {
			mirrors = usable
		}
	}
	return mirrors
}

func isCanonicalFanguoCategory(category string) bool {
	for _, entry := range fanguoCategoryNames {
		if entry.ID == category {
			return true
		}
	}
	return false
}

type fanguoAPIError struct {
	status    int
	permanent bool
	err       error
}

func (e *fanguoAPIError) Error() string { return e.err.Error() }
func (e *fanguoAPIError) Unwrap() error { return e.err }

func (d *Downloader) fanguoGetOnce(ctx context.Context, base string, query url.Values, limit int64) (any, error) {
	parsed, err := url.Parse(base)
	if err != nil || !isProviderHTTPMediaURL(base) || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("番果接口地址无效")
	}
	address := base
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("无法创建番果请求")
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	request.Header.Set("Referer", parsed.Scheme+"://"+parsed.Host+"/")
	if d.limiter != nil {
		release, err := d.limiter.acquire(ctx, request)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	transport := *d.client
	transport.Jar = nil
	previousRedirect := transport.CheckRedirect
	transport.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || providerMediaOrigin(next.URL) != providerMediaOrigin(parsed) {
			return errors.New("番果接口重定向地址异常")
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		return nil
	}
	response, err := transport.Do(request)
	if err != nil {
		return nil, fmt.Errorf("番果连接失败：%w", publicError(err))
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if d.limiter != nil {
		d.limiter.observe(request, response)
	}
	observeSourceResponse(ctx, response)
	blocked := catalogResponseBlockReason(response, data) != ""
	if response.StatusCode < 200 || response.StatusCode >= 300 || blocked {
		return nil, &fanguoAPIError{
			status:    response.StatusCode,
			permanent: !blocked && response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != 408 && response.StatusCode != 429,
			err:       d.catalogResponseError(request, response, data),
		}
	}
	if readErr != nil || int64(len(data)) > limit {
		return nil, errors.New("番果返回的数据过大或读取失败")
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, errors.New("番果返回的数据格式无效")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("番果返回的数据格式无效")
	}
	if row, ok := decoded.(map[string]any); ok {
		// MacCMS 采集接口用 code=1 表示成功，部分部署用 code=200；其余视为业务错误。
		if code := nativeText(row["code"]); code != "" && code != "200" && code != "1" {
			message := mapString(row, "msg", "message")
			if message == "" || message == "数据列表" {
				message = "上游返回错误码 " + code
			}
			return nil, errors.New("番果接口错误：" + truncate(message, 120))
		}
	}
	return decoded, nil
}

func fanguoDramaFromMap(row map[string]any) (Drama, error) {
	if row == nil {
		return Drama{}, errors.New("番果返回的剧集信息不完整")
	}
	id := mapString(row, "vod_id", "vod_id_string")
	title := strings.TrimSpace(mapString(row, "vod_name"))
	if !fanguoNumericID(id) || title == "" {
		return Drama{}, errors.New("番果返回的剧集信息不完整")
	}
	description := truncate(fanguoStripTags(mapString(row, "vod_blurb", "vod_content")), 12000)
	cover := providerCoverAddress(row["vod_pic"], fanguoMirrors[0].base+"/")
	episodes := 0
	if total, err := strconv.Atoi(strings.TrimSpace(mapString(row, "vod_total"))); err == nil && total > episodes && total <= 100000 {
		episodes = total
	}
	category := strings.TrimSpace(mapString(row, "vod_type"))
	if category == "" {
		category = "番剧"
	}
	tags := make([]string, 0, 8)
	seen := map[string]bool{}
	addTag := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] || len([]rune(value)) > 48 || len(tags) >= 16 {
			return
		}
		seen[value] = true
		tags = append(tags, value)
	}
	for _, value := range strings.Split(mapString(row, "vod_class"), ",") {
		addTag(truncate(fanguoStripTags(value), 48))
	}
	if remarks := mapString(row, "vod_remarks"); remarks != "" {
		addTag(truncate(remarks, 48))
	}
	if area := mapString(row, "vod_area"); area != "" {
		addTag(area)
	}
	if year := leadingYear(mapString(row, "vod_time", "vod_year", "vod_pubdate")); year != "" {
		addTag(year + "年")
	}
	score := mapString(row, "vod_douban_score")
	if value, err := strconv.ParseFloat(score, 64); err != nil || value <= 0 || value > 10 {
		score = ""
	} else {
		score = truncate(score, 16)
		addTag("评分 " + score)
	}
	return Drama{
		ID: providerDramaID(sourceFanguo, id), Source: sourceFanguo, SourceID: id,
		Title: truncate(title, 256), Name: truncate(title, 256),
		Desc: description, Intro: description,
		Cover: cover, CoverURL: cover,
		TotalEpisode: episodes, EpisodeCount: episodes,
		Category: category, ChannelName: "番果",
		Tags: tags, Score: score,
	}, nil
}

// fetchFanguoCategories 返回固定规范分类：各镜像 t 不同，剧集 ID 也不互通，
// 因此对外只用规范 ID，由 fanguoMirrorCandidates 映射到当前镜像。
func (d *Downloader) fetchFanguoCategories(ctx context.Context) ([]nativeCategory, error) {
	return append([]nativeCategory(nil), fanguoCategoryNames...), nil
}

func (d *Downloader) fetchFanguoCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	if page < 1 || page > fanguoMaxPage {
		return nil, false, errors.New("番果目录页码无效")
	}
	if query != "" && len(query) > fanguoMaxKeyword {
		return nil, false, errors.New("番果搜索关键词过长")
	}
	if query == "" && !validFanguoCategory(category) {
		return nil, false, errors.New("番果内容分类无效")
	}
	value, err := d.fanguoGet(ctx, category, func(mirror fanguoMirror) url.Values {
		values := url.Values{"ac": {"videolist"}, "pg": {strconv.Itoa(page)}, "limit": {strconv.Itoa(fanguoPageSize)}}
		if query != "" {
			values.Set("wd", query)
		} else if entry, ok := mirror.categories[category]; ok {
			values.Set("t", entry)
		} else if category != "" {
			values.Set("t", category)
		}
		return values
	}, 16<<20)
	if err != nil {
		return nil, false, err
	}
	root, _ := value.(map[string]any)
	if root == nil {
		return nil, false, errors.New("番果目录数据格式无效")
	}
	rows, _ := root["list"].([]any)
	if len(rows) > 500 {
		return nil, false, errors.New("番果目录数据超出上限")
	}
	hasMore := len(rows) >= fanguoPageSize/2
	if total, err := strconv.Atoi(nativeText(root["total"])); err == nil && total >= 0 && total <= 100000000 {
		hasMore = page*fanguoPageSize < total
	}
	items := make([]Drama, 0, len(rows))
	seen := map[string]bool{}
	byTitle := map[string]int{}
	episodeCounts := make([]int, 0, len(rows))
	for _, entry := range rows {
		row, _ := entry.(map[string]any)
		drama, err := fanguoDramaFromMap(row)
		if err != nil {
			return nil, false, err
		}
		if seen[drama.ID] {
			continue
		}
		seen[drama.ID] = true
		key := fanguoTitleKey(drama.Title)
		count, _ := strconv.Atoi(nativeText(drama.EpisodeCount))
		if index, ok := byTitle[key]; ok {
			// 同一部剧被采集站拆成多条时，只保留集数更多的一条。
			if count > episodeCounts[index] {
				items[index] = drama
				episodeCounts[index] = count
			}
			continue
		}
		byTitle[key] = len(items)
		items = append(items, drama)
		episodeCounts = append(episodeCounts, count)
	}
	if len(items) == 0 && hasMore {
		return nil, false, errors.New("番果未返回应有的目录页，请重试")
	}
	return items, hasMore, nil
}

// fanguoSplitEntries 按 MacCMS 的 # 分隔线路内分集，兼容个别部署改用逗号的情况。
func fanguoSplitEntries(group string) []string {
	if entries := strings.Split(group, "#"); len(entries) > 1 {
		return entries
	}
	return strings.Split(group, ",")
}

// fanguoPickLine 选集数最全的线路；集数相同时优先名称含 m3u8 的直连线。
func fanguoPickLine(from []string, groups []string) (int, []string) {
	best, bestCount := -1, 0
	for index := range groups {
		if groups[index] == "" {
			continue
		}
		count := len(fanguoSplitEntries(groups[index]))
		preferred := index < len(from) && strings.Contains(strings.ToLower(from[index]), "m3u8")
		if count > bestCount || (count == bestCount && best >= 0 && preferred &&
			(best >= len(from) || !strings.Contains(strings.ToLower(from[best]), "m3u8"))) {
			best, bestCount = index, count
		}
	}
	if best >= 0 {
		return best, fanguoSplitEntries(groups[best])
	}
	return 0, nil
}

func fanguoChaptersFromDrama(drama Drama, row map[string]any) ([]Chapter, error) {
	from := strings.Split(mapString(row, "vod_play_from"), "$$$")
	groups := strings.Split(mapString(row, "vod_play_url"), "$$$")
	lineIndex, entries := fanguoPickLine(from, groups)
	if len(entries) == 0 {
		return nil, errors.New("番果该剧暂时没有可播放的分集")
	}
	chapters := make([]Chapter, 0, len(entries))
	seenIDs, seenOrders := map[string]bool{}, map[int]bool{}
	for index, entry := range entries {
		title, address, found := strings.Cut(entry, "$")
		if !found {
			continue
		}
		playURL := fanguoPlayURL(strings.TrimSpace(address))
		if playURL == "" {
			continue
		}
		order := index + 1
		if digits := reFanguoEpisode.FindString(title); digits != "" {
			if parsed, err := strconv.ParseFloat(digits, 64); err == nil && parsed > 0 && parsed <= 100000 {
				order = int(parsed)
				for seenOrders[order] {
					order++
				}
			}
		}
		episodeID := fmt.Sprintf("L%dE%d", lineIndex, index)
		if seenIDs[episodeID] || seenOrders[order] {
			continue
		}
		seenIDs[episodeID], seenOrders[order] = true, true
		if strings.TrimSpace(title) == "" {
			title = fmt.Sprintf("第 %d 集", order)
		}
		referer := ""
		if parsed, parseErr := url.Parse(playURL); parseErr == nil {
			referer = parsed.Scheme + "://" + parsed.Host + "/"
		}
		chapters = append(chapters, Chapter{
			ID: providerChapterID(sourceFanguo, drama.SourceID, episodeID), Source: sourceFanguo,
			Title: truncate(title, 128), CurrentEpisode: json.RawMessage(strconv.Itoa(order)),
			VideoURL: "fanguo-play://" + url.QueryEscape(playURL),
			Referer:  referer,
		})
	}
	if len(chapters) == 0 {
		return nil, errors.New("番果该剧暂时没有可播放的分集")
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		left, _ := strconv.Atoi(chapters[i].EpisodeString(i + 1))
		right, _ := strconv.Atoi(chapters[j].EpisodeString(j + 1))
		if left == right {
			return chapters[i].ID < chapters[j].ID
		}
		return left < right
	})
	return chapters, nil
}

func (d *Downloader) fetchFanguoDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	if !fanguoNumericID(sourceID) {
		return Drama{}, nil, errors.New("番果剧集 ID 无效")
	}
	value, err := d.fanguoGet(ctx, "", func(fanguoMirror) url.Values {
		return url.Values{"ac": {"videolist"}, "ids": {sourceID}}
	}, 16<<20)
	if err != nil {
		return Drama{}, nil, err
	}
	root, _ := value.(map[string]any)
	rows, _ := root["list"].([]any)
	if root == nil || len(rows) != 1 {
		return Drama{}, nil, errors.New("番果详情数据格式无效")
	}
	row, _ := rows[0].(map[string]any)
	drama, err := fanguoDramaFromMap(row)
	if err != nil {
		return Drama{}, nil, err
	}
	if drama.SourceID != sourceID {
		return Drama{}, nil, errors.New("番果详情与请求剧集不符")
	}
	chapters, err := fanguoChaptersFromDrama(drama, row)
	if err != nil {
		return Drama{}, nil, err
	}
	if drama.TotalEpisode == 0 {
		drama.TotalEpisode = len(chapters)
		drama.EpisodeCount = len(chapters)
	}
	return drama, chapters, nil
}

// resolveFanguoMedia 把详情里记录的播放页换成 M3U8 直链；换不到时刷新详情重取。
func (d *Downloader) resolveFanguoMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, sourceID, valid := splitProviderDramaID(task.DramaID)
	prefix := providerChapterID(sourceFanguo, sourceID, "")
	if !valid || source != sourceFanguo || !fanguoNumericID(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return providerMedia{}, errors.New("番果播放分集信息无效，请刷新详情")
	}
	playURL := ""
	if raw := strings.TrimPrefix(task.Chapter.VideoURL, "fanguo-play://"); raw != task.Chapter.VideoURL {
		if decoded, err := url.QueryUnescape(raw); err == nil {
			playURL = fanguoPlayURL(decoded)
		}
	}
	if playURL == "" {
		_, chapters, err := d.fetchFanguoDetail(ctx, sourceID)
		if err != nil {
			return providerMedia{}, fmt.Errorf("刷新番果分集失败: %w", err)
		}
		for _, fresh := range chapters {
			if fresh.ID == task.Chapter.ID {
				if decoded, err := url.QueryUnescape(strings.TrimPrefix(fresh.VideoURL, "fanguo-play://")); err == nil {
					playURL = fanguoPlayURL(decoded)
				}
				break
			}
		}
		if playURL == "" {
			return providerMedia{}, errors.New("原分集已变化，请更新该剧后重新播放")
		}
	}
	referer := ""
	if parsed, err := url.Parse(playURL); err == nil {
		referer = parsed.Scheme + "://" + parsed.Host + parsed.Path
	}
	if index := strings.LastIndex(referer, "/"); index > len("https://") {
		referer = referer[:index+1]
	}
	candidate := playURL
	if !strings.HasSuffix(strings.ToLower(candidate), ".m3u8") {
		candidate = strings.TrimRight(candidate, "/") + "/index.m3u8"
	}
	playlist, finalURL, err := d.fetchMediaPlaylist(ctx, candidate, referer)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(playlist, "\ufeff")), "#EXTM3U") {
		body, pageErr := d.fetchProviderText(ctx, playURL, referer)
		if pageErr != nil {
			if err != nil {
				return providerMedia{}, fmt.Errorf("获取番果播放列表失败：%w", err)
			}
			return providerMedia{}, fmt.Errorf("获取番果播放列表失败：%w", pageErr)
		}
		match := reFanguoM3U8URL.FindString(body)
		if match == "" {
			return providerMedia{}, errors.New("番果播放页未返回有效 M3U8，线路可能已变化")
		}
		playlist, finalURL, err = d.fetchMediaPlaylist(ctx, match, referer)
		if err != nil {
			return providerMedia{}, fmt.Errorf("获取番果播放列表失败：%w", err)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(playlist, "\ufeff")), "#EXTM3U") {
		return providerMedia{}, errors.New("番果未返回有效 M3U8，链接可能已失效")
	}
	return providerMedia{URL: finalURL, Referer: referer, Playlist: playlist, Duration: m3u8Duration(playlist)}, nil
}
