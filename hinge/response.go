package hinge

import "net/http"

// Response 响应定制壳：业务层返回 Response[R] 时，内核应用 Status/Headers/Cookies
// 后，Data 仍走统一 envelope（{code, data, msg} 等）。
// 例：return hinge.Response[User]{Status: 201, Headers: ..., Data: u}, nil
type Response[R any] struct {
	Status  int
	Headers map[string]string
	Cookies []*http.Cookie
	Data    R
}

// ResponseWrapper 内核识别接口：泛型实例通过该接口被统一处理。
type ResponseWrapper interface {
	ResponseStatus() int
	ResponseHeaders() map[string]string
	ResponseCookies() []*http.Cookie
	ResponseData() any
}

func (r Response[R]) ResponseStatus() int                { return r.Status }
func (r Response[R]) ResponseHeaders() map[string]string { return r.Headers }
func (r Response[R]) ResponseCookies() []*http.Cookie    { return r.Cookies }
func (r Response[R]) ResponseData() any                  { return r.Data }

// responseNil 类型化 nil 自报接口：Response 各方法为值接收者，故 *Response[R]
// 的方法集同样包含 ResponseWrapper；handler 返回 (*Response[T])(nil) 时断言
// 会成立，随后调用 ResponseStatus 即解引用 nil 指针 panic。
// 由指针形态自报 nil 供内核拦截，从而无需在请求管线引入反射。
type responseNil interface {
	responseIsNil() bool
}

// responseIsNil 报告接收者是否为 nil（只比较指针，不解引用字段，nil 安全）。
func (r *Response[R]) responseIsNil() bool { return r == nil }
