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
