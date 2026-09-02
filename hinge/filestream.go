package hinge

import (
	"io"
	"time"
)

// FileStream 二进制下载响应。内核识别该类型后经 Sink.WriteStream 直接输出流，
// 数据源可以是文件、go:embed 内存数据或任何 io.Reader。
type FileStream struct {
	Name        string // 下载文件名（Content-Disposition）
	Size        int64  // 内容长度
	ContentType string // 如 application/octet-stream、text/plain
	Reader      io.Reader
	// ---- 以下为可选增强字段，零值保持基础行为 ----
	ModTime      time.Time // 最后修改时间；零值忽略。Reader 可 Seek 且 Size>0 时驱动 Last-Modified 与 If-Modified-Since 条件请求
	ETag         string    // 实体标签；空忽略。配合条件请求实现 304（强弱格式由调用方决定）
	Disposition  string    // Content-Disposition 类型：""/attachment（默认）| inline（浏览器内联预览）
	CacheControl string    // Cache-Control 头；空不输出（如 "private, max-age=3600"）
}
