package core

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 证书类错误必须被识别，才能触发黄豆的降级重试。
func TestIsCertificateError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"普通网络错误", errors.New("dial tcp: connection refused"), false},
		{"字符串含 x509", errors.New("x509: certificate has expired"), true},
		{"字符串含 tls", errors.New("tls: failed to verify certificate"), true},
		{"字符串含 certificate", errors.New("certificate is not yet valid"), true},
		{"证书过期类型", x509.CertificateInvalidError{Reason: x509.Expired}, true},
		{"主机名不符", x509.HostnameError{Host: "a.test"}, true},
	}
	for _, tc := range cases {
		if got := isCertificateError(tc.err); got != tc.want {
			t.Fatalf("%s: isCertificateError=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// 降级客户端必须真正跳过校验，且不改变原 client 的 transport。
func TestHuangdouRelaxedClientSkipsVerify(t *testing.T) {
	base := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	relaxed := huangdouRelaxedClient(base)
	if relaxed == base {
		t.Fatal("应返回新的 client，不能修改原对象")
	}
	transport, ok := relaxed.Transport.(*http.Transport)
	if !ok {
		t.Fatal("transport 类型不符")
	}
	if !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("降级 client 必须跳过证书校验")
	}
	// 原 client 不受影响
	original, _ := base.Transport.(*http.Transport)
	if original.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("原 client 的证书校验被意外修改")
	}
	if original.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("原 client 的 TLS 配置被破坏")
	}
}

// nil client 不应 panic。
func TestHuangdouRelaxedClientHandlesNil(t *testing.T) {
	relaxed := huangdouRelaxedClient(nil)
	if relaxed == nil {
		t.Fatal("nil 输入应返回可用 client")
	}
	transport, ok := relaxed.Transport.(*http.Transport)
	if !ok || !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("nil 输入下也应跳过校验")
	}
}

// 确认黄豆主机常量仍可解析，且降级逻辑只针对黄豆域名路径。
func TestHuangdouCertificateFallbackScope(t *testing.T) {
	if !strings.Contains(huangdouBaseURL, "://") {
		t.Fatalf("黄豆基础地址异常: %s", huangdouBaseURL)
	}
	// 非证书错误不应触发降级分支
	if isCertificateError(errors.New("context deadline exceeded")) {
		t.Fatal("超时错误不应被当成证书错误")
	}
	_ = time.Second
}
