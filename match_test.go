package main

import "testing"

func TestMatcher(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		pattern string
		want    bool
	}{
		// 精确匹配
		{"exact match", "hello", "hello", true},
		{"exact mismatch", "hello", "world", false},
		{"empty both", "", "", true},
		{"empty pattern non-empty target", "a", "", false},
		{"empty target non-empty pattern", "", "a", false},

		// % 通配符
		{"percent full", "hello", "%", true},
		{"percent prefix", "hello", "h%", true},
		{"percent suffix", "hello", "%o", true},
		{"percent middle", "hello", "h%o", true},
		{"percent empty", "hello", "hello%", true},
		{"percent empty start", "hello", "%hello", true},
		{"percent only", "", "%", true},
		{"percent double", "hello", "h%%o", true},
		{"percent triple empty", "", "%%%", true},
		{"percent mismatch", "hello", "a%", false},
		{"percent missing end", "hello", "%a", false},

		// _ 通配符
		{"underscore exact", "a", "_", true},
		{"underscore longer", "ab", "_", false},
		{"underscore prefix", "hello", "_ello", true},
		{"underscore suffix", "hello", "hell_", true},
		{"underscore middle", "hello", "he_lo", true},
		{"underscore multiple", "abc", "___", true},
		{"underscore too few", "abc", "__", false},
		{"underscore too many", "abc", "____", false},
		{"underscore with empty target", "", "_", false},

		// [] 字符类
		{"class match", "hello", "h[aeiou]llo", true},
		{"class mismatch", "hello", "h[xyz]llo", false},
		{"class first", "apple", "[aeiou]pple", true},
		{"class last", "banana", "bana[na]", false},
		{"class single", "a", "[abc]", true},
		{"class single mismatch", "d", "[abc]", false},
		{"class empty", "", "[]", false},
		{"class empty target", "", "[a]", false},
		{"class unclosed", "abc", "[abc", false},

		// 组合
		{"mix percent underscore", "abc", "a_%c", true},
		{"mix underscore class", "abc", "_[bc]c", true},
		{"mix all", "hello world", "h%_o [wxyz]orld", true},
		{"mix complex", "abcdef", "a%_c[e-g]f", false},

		// 中文 / Unicode
		{"chinese exact", "你好世界", "你好世界", true},
		{"chinese percent", "你好世界", "你好%", true},
		{"chinese underscore", "你好世界", "_好世界", true},
		{"chinese class", "你好世界", "[你他]好世界", true},
		{"chinese class mismatch", "你好世界", "[他她]好世界", false},
		{"chinese mix", "你好世界", "_好%界", true},
		{"emoji exact", "👋🌍🎉", "👋🌍🎉", true},
		{"emoji percent", "👋🌍🎉", "👋%", true},
		{"emoji underscore", "👋🌍🎉", "_🌍_", true},
		{"chinese and ascii", "hello世界", "hello%", true},
		{"chinese and ascii underscore", "a你b", "_你_", true},

		// 边界与回溯
		{"backtrack simple", "aaab", "a%b", true},
		{"backtrack greedy", "abcde", "a%c%e", true},
		{"backtrack fail", "abcde", "a%z%e", false},
		{"multiple percent", "abcdef", "a%cd%f", true},
		{"percent at end only", "abc", "abc%", true},
		{"percent at start only", "abc", "%abc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Matcher([]byte(tt.target), []byte(tt.pattern))
			if got != tt.want {
				t.Errorf("Matcher(%q, %q) = %v, want %v", tt.target, tt.pattern, got, tt.want)
			}
		})
	}
}
