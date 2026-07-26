package main

// Matcher 判断 target 是否匹配 pattern
// 支持中文、emoji 等任意 Unicode 字符
// pattern 支持三种通配符：
//   %    匹配任意长度字符串（包括空串）
//   _    匹配恰好一个任意字符
//   [..] 匹配方括号内任意一个字符
func Matcher(target []byte, pattern []byte) bool {
	// 转为 rune 切片，按字符（码点）操作
	tr := []rune(string(target))
	pr := []rune(string(pattern))

	tLen, pLen := len(tr), len(pr)
	ti, pi := 0, 0
	starIdx := -1 // 最近一个 % 的位置
	matchIdx := 0 // % 当前匹配到的 target 位置

	for ti < tLen {
		if pi < pLen {
			switch pr[pi] {
			case '[':
				// 解析字符类 [...]
				end := pi + 1
				for end < pLen && pr[end] != ']' {
					end++
				}
				if end < pLen {
					matched := false
					for i := pi + 1; i < end; i++ {
						if pr[i] == tr[ti] {
							matched = true
							break
						}
					}
					if matched {
						pi = end + 1
						ti++
						continue
					}
				}

			case '_':
				// 匹配任意单个字符（包括中文、emoji）
				pi++
				ti++
				continue

			case '%':
				// 匹配任意长度字符串
				starIdx = pi
				matchIdx = ti
				pi++
				continue

			default:
				// 普通字符精确匹配
				if pr[pi] == tr[ti] {
					pi++
					ti++
					continue
				}
			}
		}

		// 回溯：让 % 多吞一个字符
		if starIdx != -1 {
			pi = starIdx + 1
			matchIdx++
			ti = matchIdx
			continue
		}

		return false
	}

	// 跳过尾部剩余的 %
	for pi < pLen && pr[pi] == '%' {
		pi++
	}

	return pi == pLen
}
