package cloudstorage

import (
	"math/big"
	"strings"
)

// Encode 极简版：利用 big.Int 内置的 Text 方法
func Encode(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	// 统计前导零（big.Int 会丢失字节数组前面的 0x00）
	zcount := 0
	for zcount < len(data) && data[zcount] == 0 {
		zcount++
	}

	// big.Int.Text(62) 使用的是 0-9a-zA-Z 字符集
	n := new(big.Int).SetBytes(data[zcount:])
	res := n.Text(62)

	// 补回前导零：在 62 进制中，'0' 对应 0x00
	return strings.Repeat("0", zcount) + res
}

// Decode 极简版：利用 big.Int 内置的 SetString 方法
func Decode(s string) []byte {
	if s == "" {
		return []byte{}
	}

	zcount := 0
	for zcount < len(s) && s[zcount] == '0' {
		zcount++
	}

	n := new(big.Int)
	// SetString 支持 2-62 进制
	n, ok := n.SetString(s[zcount:], 62)
	if !ok {
		return nil
	}

	payload := n.Bytes()
	res := make([]byte, zcount+len(payload))
	copy(res[zcount:], payload)
	return res
}
