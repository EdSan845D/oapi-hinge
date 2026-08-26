// Package contract 框架核心契约：统一 Handler 模板、路由注册描述与基础类型。
// 业务层（app/handlers）通过别名引用本包类型，运行时（internal/server）与
// 文档生成（internal/openapi）通过本包消费路由注册表。
package contract

import (
	"context"
	"errors"
	"io"
	"net/http"
)

// NoReq 无入参的 Handler 使用该类型占位
type NoReq struct{}

// Empty 无响应数据的操作使用该类型占位（序列化为 data: null）
type Empty struct{}

// ErrNotFound 资源不存在（运行时适配器映射为 HTTP 404）
var ErrNotFound = errors.New("not found")

// FileStream 二进制下载响应。运行时适配器识别该类型后直接输出流，
// 数据源可以是文件、go:embed 内存数据或任何 io.Reader（见 app/handlers/file.go 示例）。
type FileStream struct {
	Name        string // 下载文件名（Content-Disposition）
	Size        int64  // 内容长度
	ContentType string // 如 application/octet-stream、text/plain
	Reader      io.Reader
}

// Response 响应定制壳（逃生舱 2）：业务层返回 contract.Response[R] 时，
// 适配器应用 Status/Headers/Cookies 后，Data 仍走统一 envelope {code, data, msg}。
// 例：return contract.Response[handlers.User]{Status: 201, Headers: ..., Data: u}, nil
type Response[R any] struct {
	Status  int
	Headers map[string]string
	Cookies []*http.Cookie
	Data    R
}

// ResponseWrapper 适配器识别接口：泛型实例通过该接口被统一处理
type ResponseWrapper interface {
	ResponseStatus() int
	ResponseHeaders() map[string]string
	ResponseCookies() []*http.Cookie
	ResponseData() any
}

func (r Response[R]) ResponseStatus() int            { return r.Status }
func (r Response[R]) ResponseHeaders() map[string]string { return r.Headers }
func (r Response[R]) ResponseCookies() []*http.Cookie    { return r.Cookies }
func (r Response[R]) ResponseData() any                  { return r.Data }

type frameworkKey struct{}

// WithFramework 注入框架上下文对象（逃生舱 3）：适配器在 decorate 阶段把
// gin.Context / echo.Context 存入 context，业务层按需断言使用（最后手段，
// 代价是业务层与框架耦合，仅限无法模板化的少数场景）。
func WithFramework(ctx context.Context, fw any) context.Context {
	return context.WithValue(ctx, frameworkKey{}, fw)
}

// Framework 取出框架上下文对象；未注入时返回 nil
func Framework(ctx context.Context) any {
	v, _ := ctx.Value(frameworkKey{}).(any)
	return v
}
// AdaptHandler 统一 Handler 模板：
//
//	func(ctx context.Context, query Q, body B) (resp R, err error)
type AdaptHandler[Q, B, R any] = func(context.Context, Q, B) (R, error)

// RouteMeta 路由注册描述（强类型）。
// 鉴权等中间件效果不在本结构声明：中间件挂载在所属 Group（见 Group.Middlewares），
// 文档标注由 internal/openapi 按中间件函数名匹配文档钩子（RegisterMiddlewareDoc）。
type RouteMeta[Q, B, R any] struct {
	Method      string
	Path        string
	Summary     string
	Description string
	Tags        []string
	Handler     AdaptHandler[Q, B, R]
}

// Route 路由表条目（非泛型；Handler 由适配器通过反射消费，Q/B/R 泛型信息由 New 在构造期保证）
type Route struct {
	Method      string
	Path        string
	Summary     string
	Description string
	Tags        []string
	Handler     any // func(context.Context, Q, B) (R, error)
}

// New 以强类型方式构造路由表条目
func New[Q, B, R any](m RouteMeta[Q, B, R]) Route {
	return Route{
		Method:      m.Method,
		Path:        m.Path,
		Summary:     m.Summary,
		Description: m.Description,
		Tags:        m.Tags,
		Handler:     m.Handler,
	}
}

type userKey struct{}

// WithUser 将当前用户注入上下文（由运行时适配器的 ContextDecorator 调用）
func WithUser(ctx context.Context, user any) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// ErrNoUser 当前用户未注入
var ErrNoUser = errors.New("current user not found")

// CurrentUser 从上下文取出当前用户
func CurrentUser(ctx context.Context) (any, error) {
	user, ok := ctx.Value(userKey{}).(any)
	if !ok || user == nil {
		return nil, ErrNoUser
	}
	return user, nil
}