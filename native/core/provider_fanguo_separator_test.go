package core

import (
	"strings"
	"testing"
)

// 真实上游形态：线路间 $$$ 分隔，线路内分集 # 分隔。
const fanguoHashPlayURL = "第1集$https://vv.jisuzyv.com/play/aADmpwOe#第2集$https://vv.jisuzyv.com/play/dwpE9gMe#" +
	"第3集$https://vv.jisuzyv.com/play/eXD7Ygoe#第4集$https://vv.jisuzyv.com/play/aOYERYGd#" +
	"第5集$https://vv.jisuzyv.com/play/ejRv1jle#第6集$https://vv.jisuzyv.com/play/eZ6w5oEe#" +
	"第7集$https://vv.jisuzyv.com/play/eXDYr9le" +
	"$$$" +
	"第1集$https://vv.jisuzyv.com/play/aADmpwOe/index.m3u8#第2集$https://vv.jisuzyv.com/play/dwpE9gMe/index.m3u8#" +
	"第3集$https://vv.jisuzyv.com/play/eXD7Ygoe/index.m3u8#第4集$https://vv.jisuzyv.com/play/aOYERYGd/index.m3u8#" +
	"第5集$https://vv.jisuzyv.com/play/ejRv1jle/index.m3u8#第6集$https://vv.jisuzyv.com/play/eZ6w5oEe/index.m3u8#" +
	"第7集$https://vv.jisuzyv.com/play/eXDYr9le/index.m3u8"

func TestFanguoChaptersUseHashSeparator(t *testing.T) {
	row := map[string]any{
		"vod_id":        float64(153821),
		"vod_name":      "学生会也有洞！",
		"vod_total":     "12",
		"vod_remarks":   "第7集",
		"vod_play_from": "jsyun$$$jsm3u8",
		"vod_play_url":  fanguoHashPlayURL,
	}
	drama, err := fanguoDramaFromMap(row)
	if err != nil {
		t.Fatalf("fanguoDramaFromMap: %v", err)
	}
	chapters, err := fanguoChaptersFromDrama(drama, row)
	if err != nil {
		t.Fatalf("fanguoChaptersFromDrama: %v", err)
	}
	if len(chapters) != 7 {
		t.Fatalf("期望 7 集，实得 %d", len(chapters))
	}
	for _, chapter := range chapters {
		if !strings.Contains(chapter.VideoURL, "index.m3u8") {
			t.Fatalf("未优先选择 m3u8 直连线: %s", chapter.VideoURL)
		}
	}
	if chapters[6].Title != "第7集" {
		t.Fatalf("末集标题异常: %s", chapters[6].Title)
	}
}

func TestFanguoChaptersCommaFallback(t *testing.T) {
	row := map[string]any{
		"vod_id":        float64(1),
		"vod_name":      "测试",
		"vod_play_from": "x",
		"vod_play_url":  "第1集$https://a.test/1.m3u8,第2集$https://a.test/2.m3u8",
	}
	drama, err := fanguoDramaFromMap(row)
	if err != nil {
		t.Fatal(err)
	}
	chapters, err := fanguoChaptersFromDrama(drama, row)
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 2 {
		t.Fatalf("逗号兼容失效：期望 2 集，实得 %d", len(chapters))
	}
}

func TestFanguoPickLineChoosesLongest(t *testing.T) {
	from := []string{"short", "longm3u8"}
	groups := []string{
		"第1集$https://a.test/1.m3u8",
		"第1集$https://b.test/1.m3u8#第2集$https://b.test/2.m3u8#第3集$https://b.test/3.m3u8",
	}
	index, entries := fanguoPickLine(from, groups)
	if index != 1 || len(entries) != 3 {
		t.Fatalf("应选最长线路：index=%d entries=%d", index, len(entries))
	}
}
