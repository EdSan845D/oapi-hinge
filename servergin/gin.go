// Package server 运行时适配器：把统一路由注册表(routes.All())挂载到原生 Gin。
// 职责：上下文装饰 -> 参数绑定 -> 入参转换 -> 校验 -> Handler 调用 ->
// 出参转换 -> 状态码决策 -> 响应壳包装 -> 写出。
// 设计目标：业务层零框架依赖；解析/校验/错误映射/响应壳均可扩展。
package servergin

import (
	"context"
	"net/http"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/response"
	"github.com/EdSan845D/oapi-hinge/contract/validator"

	"github.com/gin-gonic/gin"
)

// Server 运行时服务器装配器
type Server struct {
	middlewares []gin.HandlerFunc
	// 校验器：绑定后按注册顺序执行（内置标签必填校验 + Validate() 方法在绑定阶段完成）
	validators []validator.Func
	// 错误映射：默认 ErrNotFound -> 404，其余业务错误 -> 200 + code:7。
	// 优先级低于错误自带状态码（contract.StatusError / contract.StatusCoder）。
	mapError func(err error) (httpStatus, bizCode int)
	// 上下文装饰：把 gin 上下文中的用户/claims 注入 handler 的 context.Context
	decorate func(c *gin.Context, ctx context.Context) context.Context
	// 响应壳：成功/失败统一经过其包装（默认 {code, data, msg}）
	envelope response.Envelope
	// 绑定/校验失败的 HTTP 状态码（默认 200，存量行为）；SetBindErrorStatus 可改
	bindStatus int
}

// New 创建 Server
func New() *Server {
	s := &Server{}
	s.mapError = defaultErrorMapper
	s.decorate = func(c *gin.Context, ctx context.Context) context.Context {
		return contract.WithFramework(ctx, c)
	}
	s.envelope = response.DefaultEnvelope{}
	s.bindStatus = http.StatusOK
	return s
}

// Use 扩展点：挂载全局中间件（鉴权/CORS/限流等），在业务路由之前执行。
// 按分组挂载的中间件请直接写在 contract.Group.Middlewares（随树继承）。
func (s *Server) Use(mw ...gin.HandlerFunc) *Server {
	s.middlewares = append(s.middlewares, mw...)
	return s
}

// AddValidator 扩展点：注册自定义校验器（绑定后执行），
// 见 validator 包：内置标签必填 + Validate() 接口调用；validator.Playground() 接入完整校验规则
func (s *Server) AddValidator(v validator.Func) *Server {
	s.validators = append(s.validators, v)
	return s
}

// SetErrorMapper 扩展点：自定义 错误 -> (HTTP状态码, 业务code) 映射。
// 仅对不携带状态码的普通错误生效（StatusError / StatusCoder 优先）。
func (s *Server) SetErrorMapper(fn func(err error) (httpStatus, bizCode int)) *Server {
	s.mapError = fn
	return s
}

// SetContextDecorator 扩展点：把 gin 上下文信息注入 context.Context。
// 在每次请求最前执行（Q/B 绑定之前），TransformIn / 校验器 / TransformOut / handler
// 共享同一个已装饰 ctx。约定：fn 必须是纯派生（只读 c、轻量 WithValue 级操作），
// 重操作（鉴权、查库）请放中间件——每个请求（含校验失败的请求）都会执行。
func (s *Server) SetContextDecorator(fn func(c *gin.Context, ctx context.Context) context.Context) *Server {
	s.decorate = fn
	return s
}

// SetEnvelope 扩展点：自定义响应壳。
// 传入 nil 恢复默认壳 {code, data, msg}；路由级覆盖见 contract.RouteMeta.Envelope。
// 文档侧请用 openapi.OptionWithEnvelopeSchema 配对配置（两者独立）。
func (s *Server) SetEnvelope(env response.Envelope) *Server {
	if env != nil {
		s.envelope = env
	}
	return s
}

// SetBindErrorStatus 扩展点：设置参数绑定/校验失败（含 InTransform 错误）的 HTTP 状态码。
// 默认 200（存量行为：HTTP 200 + code=CodeError）；设为 400 可获得 RESTful 语义，
// 非 200 时业务 code 跟随状态码（与 StatusError 约定一致）。
// 仅影响绑定/校验阶段；Handler 返回的业务错误走 StatusError / SetErrorMapper，不受影响。
func (s *Server) SetBindErrorStatus(status int) *Server {
	if status > 0 {
		s.bindStatus = status
	}
	return s
}

// Mount 把路由分组树挂载到 gin.RouterGroup。
// 组中间件就地断言为 gin.HandlerFunc 后 Use（gin 自动向子组继承）。
func (s *Server) Mount(g *gin.RouterGroup, groups []*contract.Group) {
	api := g.Group("")
	if len(s.middlewares) > 0 {
		api.Use(s.middlewares...)
	}
	for _, grp := range groups {
		s.mountGroup(api, grp)
	}
}
