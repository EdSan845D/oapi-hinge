// Package contract 框架核心契约：统一 Handler 模板、路由注册描述与基础类型。
// 业务层（app/handlers）通过别名引用本包类型，运行时（internal/server）与
// 文档生成（internal/openapi）通过本包消费路由注册表。
package contract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"

	"github.com/EdSan845D/oapi-hinge/contract/response"
)

// NoReq 无入参的 Handler 使用该类型占位
type NoReq any

// Empty 无响应数据的操作使用该类型占位（序列化为 data: null）
type Empty any

// ErrNotFound 资源不存在（运行时适配器映射为 HTTP 404）。
// 需要携带对外信息时优先使用 contract.NotFound(msg)（StatusError）。
var ErrNotFound = errors.New("not found")

// FileStream 二进制下载响应。运行时适配器识别该类型后直接输出流，
// 数据源可以是文件、go:embed 内存数据或任何 io.Reader（见 app/handlers/file.go 示例）。
type FileStream struct {
	Name        string // 下载文件名（Content-Disposition）
	Size        int64  // 内容长度
	ContentType string // 如 application/octet-stream、text/plain
	Reader      io.Reader
}

// Response 响应定制壳：业务层返回 contract.Response[R] 时，
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

func (r Response[R]) ResponseStatus() int                { return r.Status }
func (r Response[R]) ResponseHeaders() map[string]string { return r.Headers }
func (r Response[R]) ResponseCookies() []*http.Cookie    { return r.Cookies }
func (r Response[R]) ResponseData() any                  { return r.Data }

type frameworkKey struct{}

// WithFramework 注入框架上下文对象：适配器在 decorate 阶段把
// gin.Context / echo.Context 存入 context，业务层按需断言使用
// 代价是业务层与框架耦合，仅限无法模板化的少数场景。
func WithFramework(ctx context.Context, fw any) context.Context {
	return context.WithValue(ctx, frameworkKey{}, fw)
}

// Framework 取出框架上下文对象；未注入时返回 nil
func Framework(ctx context.Context) any {
	return ctx.Value(frameworkKey{})
}

// AdaptHandler 统一 Handler 模板：
//
//	func(ctx context.Context, query Q, body B) (resp R, err error)
type AdaptHandler[Q, B, R any] = func(context.Context, Q, B) (R, error)

var (
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType   = reflect.TypeOf((*error)(nil)).Elem()
)

// CheckHandler 校验 Handler 是否符合统一模板 func(context.Context, Q, B) (R, error)。
// 适配器与文档生成器在挂载/生成期调用：签名错误尽早暴露并给出可读信息，
// 避免拖到反射调用期才 panic 且信息晦涩。
func CheckHandler(fn any) error {
	if fn == nil {
		return errors.New("handler is nil")
	}
	t := reflect.TypeOf(fn)
	if t.Kind() != reflect.Func {
		return fmt.Errorf("handler is %s, want func", t)
	}
	if t.NumIn() != 3 {
		return fmt.Errorf("handler has %d inputs, want 3 (context.Context, Q, B)", t.NumIn())
	}
	if t.In(0) != contextType {
		return fmt.Errorf("handler first input is %s, want context.Context", t.In(0))
	}
	if t.NumOut() != 2 {
		return fmt.Errorf("handler has %d outputs, want 2 (R, error)", t.NumOut())
	}
	if !t.Out(1).Implements(errorType) {
		return fmt.Errorf("handler second output is %s, want error", t.Out(1))
	}
	return nil
}

// RouteMeta 路由注册描述（强类型）。
// 鉴权等中间件效果不在本结构声明：中间件挂载在所属 Group（见 Group.Middlewares），
// 文档标注由 internal/openapi 按中间件函数名匹配文档钩子（RegisterMiddlewareDoc）。
type RouteMeta[Q, B, R any] struct {
	Method      string
	Path        string
	Summary     string
	Description string
	Tags        []string
	// DefaultStatusCode 成功响应默认 HTTP 状态码；0 → 200。
	// 动态覆盖优先级：contract.Response[R].Status（逃生舱 2）> DefaultStatusCode > 200。
	// OpenAPI 文档生成器读取该值作为成功响应码（替代硬编码 200）。
	DefaultStatusCode int
	// Deprecated 弃用标记（运行时+文档共用层）：
	// 文档生成 op.Deprecated=true；运行时后续可在 dev 模式对弃用接口打日志。
	Deprecated bool
	// Envelope 路由级响应壳；nil → 服务级默认壳（server.SetEnvelope）。
	// 文档侧壳 schema 用 openapi.OptionWithEnvelopeSchema 配对配置。
	Envelope response.Envelope
	Handler  AdaptHandler[Q, B, R]
}

// Route 路由表条目（非泛型；Handler 由适配器通过反射消费，Q/B/R 泛型信息由 New 在构造期保证）
type Route struct {
	Method            string
	Path              string
	Summary           string
	Description       string
	Tags              []string
	DefaultStatusCode int
	Deprecated        bool
	Envelope          response.Envelope
	Handler           any // func(context.Context, Q, B) (R, error)
}

// New 以强类型方式构造路由表条目
func New[Q, B, R any](m RouteMeta[Q, B, R]) Route {
	return Route{
		Method:            m.Method,
		Path:              m.Path,
		Summary:           m.Summary,
		Description:       m.Description,
		Tags:              m.Tags,
		DefaultStatusCode: m.DefaultStatusCode,
		Deprecated:        m.Deprecated,
		Envelope:          m.Envelope,
		Handler:           m.Handler,
	}
}
