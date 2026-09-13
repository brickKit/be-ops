package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadDotEnv_多行值真机复现 是阶段四 Task 6 真机部署
// infra-iam-casdoor 时撞到的真实 bug，分两轮才修对（见 readDotEnv 的
// 踩坑记录注释）：
//  1. brickKit CLI 生成的 local-debug 文件里，appTokenSigningKeyPem 这
//     类多行值是**原样把换行符写进文件**，不加引号也不转义——旧实现只
//     认单行 KEY=VALUE，PEM 续行因为没有 "=" 被当成无效行跳过，值被
//     截断成只剩第一行。
//  2. 第一轮修复后，PEM 在**倒数第二行**又被截断——那一行恰好是真实
//     Base64 编码结尾带 `=` 补齐符的续行（`.../FXH` 之后紧跟一个以
//     `=` 结尾的续行），"这行有没有 `=`" 这条判据会把它误判成
//     KEY=VALUE。这条用例直接用真实密钥（含这个 `=` 结尾的续行）做
//     回归，不用编造的、凑巧不带 `=` 的假数据。
func TestReadDotEnv_多行值真机复现(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local-debug.infra-iam-casdoor-1-0-7.env")
	content := `# 由 BrickKit CLI 自动生成
COMPONENT_ID=infra/iam-casdoor
APP_TOKEN_SIGNING_KEY_PEM=-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcw
ggSjAgEAAoIBAQCzLqzITq19
q5ev7DZvMsgE+nq1YPPG+zfq5Rk27L2FMl5iFayScFujjeCJMlxQXO2UJJaX/FXH
yv9RnYdxMobgOEcjeTMTqYI=
-----END PRIVATE KEY-----
CASDOOR_BASE_URL=http://host.docker.internal:8000
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	env, err := readDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}

	want := "-----BEGIN PRIVATE KEY-----\n" +
		"MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcw\n" +
		"ggSjAgEAAoIBAQCzLqzITq19\n" +
		"q5ev7DZvMsgE+nq1YPPG+zfq5Rk27L2FMl5iFayScFujjeCJMlxQXO2UJJaX/FXH\n" +
		"yv9RnYdxMobgOEcjeTMTqYI=\n" +
		"-----END PRIVATE KEY-----"
	if got := env["APP_TOKEN_SIGNING_KEY_PEM"]; got != want {
		t.Fatalf("多行值没有正确拼回来：\n期望 %q\n实际 %q", want, got)
	}
	// 多行值前后的正常单行 key 不能被拖累。
	if env["COMPONENT_ID"] != "infra/iam-casdoor" {
		t.Fatalf("COMPONENT_ID 被多行值解析逻辑污染：%q", env["COMPONENT_ID"])
	}
	if env["CASDOOR_BASE_URL"] != "http://host.docker.internal:8000" {
		t.Fatalf("多行值之后的 key 被漏读：%q", env["CASDOOR_BASE_URL"])
	}
}

func TestReadDotEnv_单行值与注释和空行(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "simple.env")
	content := "# comment\n\nCOMPONENT_ID=mdm/customer\nCOMPONENT_VERSION=\"1.0.7\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	env, err := readDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if env["COMPONENT_ID"] != "mdm/customer" {
		t.Fatalf("COMPONENT_ID=%q", env["COMPONENT_ID"])
	}
	// 原有行为：值两端的引号要去掉。
	if env["COMPONENT_VERSION"] != "1.0.7" {
		t.Fatalf("COMPONENT_VERSION=%q，引号应该被去掉", env["COMPONENT_VERSION"])
	}
}
