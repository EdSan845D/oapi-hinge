# oapi-hinge CLI（scaffold）

生成 Go API 项目骨架：统一 Handler 模板 + 原生 Gin 运行时 + 开发期 OpenAPI 文档生成（`-tags openapi` 隔离构建，release 零开发依赖）。

## 用法

```bash
# 从仓库根目录
go run ./scaffold create myapp -m github.com/you/myapp

# 或直接用发布版本
go run github.com/EdSan845D/oapi-hinge/scaffold@v0.1.0-rc.1 create myapp -m github.com/you/myapp
```

flags：`-m` 模块路径、`--no-tidy` 跳过依赖拉取、`--force` 覆盖目录。

## 生成物

统一 Handler 模板（`app/handlers`）+ 路由注册表（`app/routes`）+ 原生 Gin 运行时 + `main_doc.go` 文档入口（`-tags openapi`）。使用方式见 [docs/MANUAL.md](../docs/MANUAL.md)。

## 已知问题（v0.1.0-rc.1）

- 占位符替换未生效：生成的 module 名与 import 路径需手动修正（go.mod 同理）
- go.mod 依赖版本需手动对齐最新 tag

修复前建议按 MANUAL 第 1 节手动接入，理解三层结构后再用脚手架提效。
