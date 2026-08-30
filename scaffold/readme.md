# oapi-hinge CLI（scaffold）

生成 Go API 项目骨架：统一 Handler 模板 + 原生 Gin 运行时 + 开发期 OpenAPI 文档生成（`-tags openapi` 隔离构建，release 零开发依赖）。

## 用法

```bash
# 从仓库根目录
go run ./scaffold create myapp -m github.com/you/myapp

# 或直接用发布版本
go run github.com/EdSan845D/oapi-hinge/scaffold@latest create myapp -m github.com/you/myapp
```

flags：`-m` 模块路径、`--no-tidy` 跳过依赖拉取、`--force` 覆盖目录。

## 生成物

统一 Handler 模板（`app/handlers`）+ 路由注册表（`app/routes`）+ 原生 Gin 运行时 + `main_doc.go` 文档入口（`-tags openapi`）。

生成的 go.mod 直接依赖单模块 `github.com/EdSan845D/oapi-hinge`（模板内置 v0.2.0，可按需调整），随后自动执行 `go mod tidy`。使用方式见 [docs/MANUAL.md](../docs/MANUAL.md)。
