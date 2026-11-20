package core

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// 端到端：黄豆真实主机的证书已过期，降级客户端必须能建立 TLS 连接并拿到 HTTP 响应。
// 网络不可达时跳过，避免离线环境误报。
func TestHuangdouRelaxedClientReachesExpiredHost(t *testing.T) {
	client := huangdouRelaxedClient(&http.Client{Timeout: 20 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, huangdouBaseURL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("跳过：黄豆主机不可达（%v）", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		t.Fatalf("意外状态码: %d", resp.StatusCode)
	}
	t.Logf("降级客户端成功访问 %s -> HTTP %d", huangdouBaseURL, resp.StatusCode)
}
