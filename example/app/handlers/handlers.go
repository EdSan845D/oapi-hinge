// Package handlers 统一业务层：所有 Handler 严格遵循模板
//
//	func(ctx context.Context, query Q, body B) (resp R, err error)
//
// 本包不依赖任何 Web 框架，只依赖 context 与强类型请求/响应结构体，
// 由 internal/server（运行时）与 internal/openapi（文档生成）共同消费。
//
// 参数标签约定（两个适配器共用）：
//   - Q：query 参数用 `query:"name"` 标签，路径参数用 `path:"name"` 标签
//   - B：POST/PUT/PATCH 传业务 body 结构体；无 body 的请求传 `any`
//
// 校验（internal/server 自动执行）：
//   - 字段标签 `binding:"required"`：必填校验
//   - 结构体实现 `Validate() error` 方法：自定义校验（见 user.go CreateUserReq）
//   - 注册自定义校验器：server.AddValidator(...)
package handlers

import (
	"context"

	"github.com/EdSan845D/oapi-hinge/contract"
)

// 框架核心类型别名：业务层直接使用短名，类型本身来自 internal/contract
type (
	NoReq      = contract.NoReq
	Empty      = contract.Empty
	FileStream = contract.FileStream
)

var (
	ErrNotFound = contract.ErrNotFound
	ErrNoUser   = contract.ErrNoUser
)

// WithUser / CurrentUser：上下文用户注入与读取（配合 server.SetContextDecorator 使用）
func WithUser(ctx context.Context, user any) context.Context { return contract.WithUser(ctx, user) }
func CurrentUser(ctx context.Context) (any, error)           { return contract.CurrentUser(ctx) }