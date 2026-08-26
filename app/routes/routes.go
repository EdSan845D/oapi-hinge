// Package routes 统一路由注册表：全项目业务 API 的唯一入口。
// 运行时适配器(internal/server)与文档生成器(internal/openapi)都从 All() 取数，
// 新增接口只需：1) 在 app/handlers 写统一 Handler，2) 在 All() 注册一行。
//
// 分组约定（树形）：
//   - 组内 Route.Path 为相对组前缀的路径（列表路由用 "" 表示组根）
//   - 组 Middlewares 沿树向子组继承（运行时与文档生成行为一致）
//   - 中间件函数名即文档标识：文档钩子在 main_doc.go 按函数引用注册（可选）
package routes

import (
	"fuego-hinge/app/handlers"
	"fuego-hinge/app/middleware"
	"fuego-hinge/internal/contract"
	"fuego-hinge/internal/response"
)

// BasePath API 统一前缀
const BasePath = "/api"

// All 全部业务路由。
// 约定：无 body 的请求 B=any；无 query/path 入参的请求 Q=NoReq。
func All() []*contract.Group {
	return []*contract.Group{
		// ============ 根组 ============
		{
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[handlers.NoReq, any, map[string]string]{
					Method:  "GET",
					Path:    "/health",
					Summary: "健康检查",
					Tags:    []string{"系统"},
					Handler: handlers.Health,
				}),
			},
		},

		// ============ 用户组：/api/users（挂 auth 中间件） ============
		{
			Prefix:      "/users",
			Description: "用户相关接口",
			Tags:        []string{"用户"},
			Middlewares: []any{middleware.Auth},
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[handlers.ListUsersReq, any, response.Paged[handlers.User]]{
					Method:  "GET",
					Path:    "",
					Summary: "用户列表（分页）",
					Handler: handlers.ListUsers,
				}),
				contract.New(contract.RouteMeta[handlers.GetUserReq, any, handlers.User]{
					Method:  "GET",
					Path:    "/{id}",
					Summary: "用户详情",
					Handler: handlers.GetUser,
				}),
				contract.New(contract.RouteMeta[handlers.NoReq, handlers.CreateUserReq, handlers.User]{
					Method:  "POST",
					Path:    "",
					Summary: "创建用户",
					Handler: handlers.CreateUser,
				}),
				contract.New(contract.RouteMeta[handlers.DeleteUserReq, any, handlers.Empty]{
					Method:  "DELETE",
					Path:    "/{id}",
					Summary: "删除用户",
					Handler: handlers.DeleteUser,
				}),
			},
		},

		// ============ 文件组：/api/files（无中间件） ============
		{
			Prefix: "/files",
			Tags:   []string{"文件"},
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[handlers.DownloadSampleReq, any, *handlers.FileStream]{
					Method:  "GET",
					Path:    "/{name}",
					Summary: "下载示例文件",
					Handler: handlers.DownloadSample,
				}),
			},
		},
	}
}
