// Package main MetricAgent 主入口测试
// author: 王春
// date: 2026-09-21
package main

import "testing"

// TestCaesarDecrypt 凯撒密码解密边界测试
// 规则：A-Z / a-z 每个字母向前-1，A→Z，a→z；非字母原样保留
func TestCaesarDecrypt(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		// 基本偏移
		{"单个大写 B→A", "B", "A"},
		{"单个小写 b→a", "b", "a"},
		{"大写回卷 A→Z", "A", "Z"},
		{"小写回卷 a→z", "a", "z"},

		// 连续字母
		{"连续大写 BCDE→ABCD", "BCDE", "ABCD"},
		{"连续小写 bcde→abcd", "bcde", "abcd"},
		{"大小写混合 wXyz→xYza (即 xYza 向前一位→wXyz)", "xYza", "wXyz"},

		// 回卷场景
		{"大写开头 ZA→YZ", "ZA", "YZ"},
		{"小写开头 za→yz", "za", "yz"},

		// 非字母原样保留
		{"数字", "123", "123"},
		{"空格", "b c", "a b"},
		{"shell 符号", "$; &()", "$; &()"},
		{"标点", "b.c,d", "a.b,c"},

		// 空串
		{"空字符串", "", ""},

		// 全非字母
		{"全数字符号", "123$%^", "123$%^"},

		// PRD 示例验证
		{"PRD示例 B→A", "B", "A"},
		{"PRD示例 abcd→zabc", "abcd", "zabc"},
		{"PRD示例 xYza→wXyz", "xYza", "wXyz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := caesarDecrypt(tt.input)
			if got != tt.expect {
				t.Errorf("caesarDecrypt(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}
