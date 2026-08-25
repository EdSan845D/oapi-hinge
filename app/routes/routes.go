// Package routes 统一路由注册表：全项目业务 API 的唯一入口。
// 运行时适配器(internal/server)与文档生成器(internal/openapi)都从 All() 取数，
// 新增接口只需：1) 在 app/handlers 写统一 Handler，2) 在 All() 注册一行。
package routes

import (
	"fuego-hinge/app/handlers"
	"fuego-hinge/internal/contract"
	"fuego-hinge/internal/response"
)

// BasePath API 统一前缀
const BasePath = "/api"

// All 全部业务路由。
// 约定：无 body 的请求 B=any；无 query/path 入参的请求 Q=NoReq。
func All() []contract.Route {
	return []contract.Route{
		contract.New(contract.RouteMeta[handlers.NoReq, any, map[string]string]{
			Method:  "GET",
			Path:    "/health",
			Summary: "健康检查",
			Tags:    []string{"系统"},
			Handler: handlers.Health,
		}),
		contract.New(contract.RouteMeta[handlers.ListUsersReq, any, response.Paged[handlers.User]]{
			Method:  "GET",
			Path:    "/users",
			Summary: "用户列表（分页）",
			Tags:    []string{"用户"},
			Auth:    true,
			Handler: handlers.ListUsers,
		}),
		contract.New(contract.RouteMeta[handlers.GetUserReq, any, handlers.User]{
			Method:  "GET",
			Path:    "/users/{id}",
			Summary: "用户详情",
			Tags:    []string{"用户"},
			Auth:    true,
			Handler: handlers.GetUser,
		}),
		contract.New(contract.RouteMeta[handlers.NoReq, handlers.CreateUserReq, handlers.User]{
			Method:  "POST",
			Path:    "/users",
			Summary: "创建用户",
			Tags:    []string{"用户"},
			Auth:    true,
			Handler: handlers.CreateUser,
		}),
		contract.New(contract.RouteMeta[handlers.DeleteUserReq, any, handlers.Empty]{
			Method:  "DELETE",
			Path:    "/users/{id}",
			Summary: "删除用户",
			Tags:    []string{"用户"},
			Auth:    true,
			Handler: handlers.DeleteUser,
		}),
		contract.New(contract.RouteMeta[handlers.DownloadSampleReq, any, *handlers.FileStream]{
			Method:  "GET",
			Path:    "/files/{name}",
			Summary: "下载示例文件",
			Tags:    []string{"文件"},
			Handler: handlers.DownloadSample,
		}),
	}
}