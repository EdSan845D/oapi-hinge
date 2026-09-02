package hinge

import (
	"context"
	"mime/multipart"
	"net/http"
)

// RequestReader 框架无关的请求原始值读取接口。
// 各框架适配器（servergin / serverecho / serverhttp）实现；生成绑定器只依赖本接口，
// 从原始字符串到强类型入参的全部解析代码由 hinge gen 按字段标签生成（零反射）。
type RequestReader interface {
	// Context 原始请求 context（超时/取消传播的根）。
	Context() context.Context
	// Method HTTP 方法。
	Method() string
	// PathParam 路径参数（{id} 声明的段）。缺失返回 ("", false)。
	PathParam(name string) (string, bool)
	// QueryValues query 参数的全部取值（重复参数 ?ids=1&ids=2）。缺失返回 (nil, false)。
	QueryValues(name string) ([]string, bool)
	// Header 请求头取值。缺失/为空返回 ("", false)。
	Header(name string) (string, bool)
	// Cookie 请求 Cookie 取值。缺失返回 ("", false)。
	Cookie(name string) (string, bool)
	// Body 原始请求体字节。无 body 返回 (nil, nil)。
	Body() ([]byte, error)
	// MultipartForm 解析后的 multipart 表单（仅 multipart 请求有值）。
	// 实现方负责解析（标准库 ParseMultipartForm，32MB 内存缓冲水位）。
	MultipartForm() (*multipart.Form, error)
}

// Sink 响应写出接口：状态码 / 响应头 / Cookie / JSON / 文件流。
// 统一由内核管线调用，保证成功与失败走同一套壳与状态码决策。
type Sink interface {
	// SetStatus 设置响应状态码（WriteJSON 未调用时的最终状态）。
	SetStatus(code int)
	// SetHeader 写响应头（Response[R].Headers / 关联 ID 回写等）。
	SetHeader(k, v string)
	// AddCookie 追加 Set-Cookie（Response[R].Cookies）。
	AddCookie(c *http.Cookie)
	// WriteJSON 以指定状态码输出 JSON（实现方决定转义策略，保持各框架一致性）。
	WriteJSON(status int, v any)
	// WriteStream 输出二进制流（FileStream：Range/条件请求由实现方语义决定）。
	WriteStream(f *FileStream)
}
