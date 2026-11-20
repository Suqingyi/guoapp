package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeFanguoFixture(t *testing.T, raw string) map[string]any {
	t.Helper()
	var row map[string]any
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&row); err != nil {
		t.Fatalf("fixture 解码失败: %v", err)
	}
	return row
}

func TestFanguoDramaFromMap(t *testing.T) {
	row := decodeFanguoFixture(t, `{"vod_id":123,"vod_name":"转生成为魔剑了第二季","vod_blurb":"<p>简介</p>","vod_pic":"https://img.example.test/a.jpg","vod_type":"日本动漫","vod_class":"热血,奇幻","vod_remarks":"更新至12集","vod_area":"日本","vod_douban_score":"8.4","vod_time":"2024-10-05 12:00:00"}`)
	drama, err := fanguoDramaFromMap(row)
	if err != nil {
		t.Fatalf("映射失败: %v", err)
	}
	if drama.ID != providerDramaID(sourceFanguo, "123") || drama.Source != sourceFanguo {
		t.Fatalf("剧集标识错误: %+v", drama)
	}
	if drama.Title != "转生成为魔剑了第二季" || strings.Contains(drama.Desc, "<p>") {
		t.Fatalf("标题或简介清洗错误: %+v", drama)
	}
	if drama.Category != "日本动漫" || drama.ChannelName != "番果" {
		t.Fatalf("分类或频道错误: %+v", drama)
	}
	joined := strings.Join(drama.Tags, ",")
	for _, want := range []string{"热血", "更新至12集", "日本", "2024年", "评分 8.4"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少标签 %q: %s", want, joined)
		}
	}
}

func TestFanguoChaptersPreferRichestLine(t *testing.T) {
	row := decodeFanguoFixture(t, `{"vod_id":9,"vod_name":"测试番","vod_play_from":"jsyun$$$jsm3u8","vod_play_url":"第1集$https://vv.example.test/play/aaa$$$第1集$https://v.example.test/play/ccc,第2集$https://v.example.test/play/ddd"}`)
	drama, err := fanguoDramaFromMap(row)
	if err != nil {
		t.Fatalf("映射失败: %v", err)
	}
	chapters, err := fanguoChaptersFromDrama(drama, row)
	if err != nil {
		t.Fatalf("分集失败: %v", err)
	}
	if len(chapters) != 2 {
		t.Fatalf("分集数量错误: %d", len(chapters))
	}
	for _, chapter := range chapters {
		if !strings.Contains(chapter.ID, "L1E") {
			t.Fatalf("未优先选择集数最全的线路: %s", chapter.ID)
		}
		if !strings.HasPrefix(chapter.VideoURL, "fanguo-play://https%3A%2F%2F") {
			t.Fatalf("播放占位符错误: %s", chapter.VideoURL)
		}
		if chapter.Referer != "https://v.example.test/" {
			t.Fatalf("Referer 错误: %s", chapter.Referer)
		}
	}
}

func TestFanguoCategoryAndIDGuards(t *testing.T) {
	if !validFanguoCategory("") || !validFanguoCategory("25") || validFanguoCategory("0") || validFanguoCategory("abc") {
		t.Fatal("分类校验错误")
	}
	if !fanguoNumericID("123") || fanguoNumericID("0") || fanguoNumericID("-1") || fanguoNumericID("1e9") {
		t.Fatal("ID 校验错误")
	}
	if got := fanguoPlayURL("javascript:alert(1)"); got != "" {
		t.Fatalf("非法播放地址未过滤: %s", got)
	}
}

func TestFanguoMirrorCandidatesDedupesPreferred(t *testing.T) {
	d := &Downloader{providerHosts: map[string]string{sourceFanguo: "https://www.hongniuzy2.com"}}
	candidates := d.fanguoMirrorCandidates("")
	if len(candidates) != len(fanguoMirrors) {
		t.Fatalf("候选数量错误: %v", candidates)
	}
	if candidates[0].base != fanguoMirrors[1].base {
		t.Fatalf("有效镜像未排首位: %v", candidates)
	}
	seen := map[string]bool{}
	for _, entry := range candidates {
		if seen[entry.base] {
			t.Fatalf("候选包含重复项: %v", candidates)
		}
		seen[entry.base] = true
	}
}

func TestFanguoMirrorCandidatesFilterByCategory(t *testing.T) {
	d := &Downloader{providerHosts: map[string]string{}}
	// 数字分类在所有镜像都可用（走透传），规范分类只保留有映射的镜像。
	if got := len(d.fanguoMirrorCandidates("9")); got != len(fanguoMirrors) {
		t.Fatalf("数字分类候选数量错误: %d", got)
	}
	for _, id := range []string{"jp", "cn", "us"} {
		if got := len(d.fanguoMirrorCandidates(id)); got != len(fanguoMirrors) {
			t.Fatalf("分类 %s 候选数量错误: %d", id, got)
		}
	}
}

func TestFanguoTitleKeyDedupes(t *testing.T) {
	same := []string{"冰之城墙 第二季", "冰之城墙第二季", "冰之城墙（第二季）", "冰之城墙 第二 季"}
	key := fanguoTitleKey(same[0])
	for _, title := range same[1:] {
		if fanguoTitleKey(title) != key {
			t.Fatalf("标题未归一到同键: %q vs %q", title, same[0])
		}
	}
	if fanguoTitleKey("海贼王") == fanguoTitleKey("航海王") {
		t.Fatal("不同标题被误合并")
	}
}
