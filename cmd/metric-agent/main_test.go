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

// TestCaesarEncrypt 凯撒密码加密边界测试
// 规则：A-Z / a-z 每个字母向后+1，Z→A，z→a；非字母原样保留（与解密互逆）
// author: 王春
// date: 2026-10-14
func TestCaesarEncrypt(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		// 基本偏移
		{"单个大写 A→B", "A", "B"},
		{"单个小写 a→b", "a", "b"},
		{"大写回卷 Z→A", "Z", "A"},
		{"小写回卷 z→a", "z", "a"},

		// 连续字母
		{"连续大写 BCDE→CDEF", "BCDE", "CDEF"},
		{"连续小写 bcde→cdef", "bcde", "cdef"},
		{"大小写混合 wXyz→xYza (即 wXyz 向后一位→xYza)", "wXyz", "xYza"},

		// 回卷场景
		{"大写结尾 ZA→AB", "ZA", "AB"},
		{"小写结尾 za→ab", "za", "ab"},

		// 非字母原样保留
		{"数字", "123", "123"},
		{"空格", "a b", "b c"},
		{"shell 符号", "$; &()", "$; &()"},
		{"标点", "a.b,c", "b.c,d"},

		// 空串
		{"空字符串", "", ""},

		// 全非字母
		{"全数字符号", "123$%^", "123$%^"},

		// PRD 互逆示例验证：解密 abcd→zabc，故加密 zabc→abcd
		{"PRD互逆示例 zabc→abcd", "zabc", "abcd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := caesarEncrypt(tt.input)
			if got != tt.expect {
				t.Errorf("caesarEncrypt(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

// TestCaesarRoundTrip 凯撒加解密往返一致性测试
// 任意字符串先加密再解密（或先解密再加密）应还原为原文
// author: 王春
// date: 2026-10-14
func TestCaesarRoundTrip(t *testing.T) {
	samples := []string{
		"",
		"hello World",
		"abcXYZ 123 !@#",
		"zZ aA",
		"shell $VAR; done",
	}
	for _, s := range samples {
		if got := caesarDecrypt(caesarEncrypt(s)); got != s {
			t.Errorf("caesarDecrypt(caesarEncrypt(%q)) = %q, want %q", s, got, s)
		}
		if got := caesarEncrypt(caesarDecrypt(s)); got != s {
			t.Errorf("caesarEncrypt(caesarDecrypt(%q)) = %q, want %q", s, got, s)
		}
	}
}
