package geeCache

import "bytes"

// ByteView 表示缓存值
type ByteView struct {
	b []byte // 存储真实的缓存值
}

func (v ByteView) Len() int {
	return len(v.b)
}

// ByteSlice 返回一个拷贝, 防止缓存值被外部程序修改
func (v ByteView) ByteSlice() []byte {
	return bytes.Clone(v.b)
}

func (v ByteView) String() string {
	return string(v.b)
}
