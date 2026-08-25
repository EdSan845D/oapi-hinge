# fuego-hinge CLI

生成 Go API 项目骨架的命令行工具，用法对标 `pnpm create vite`。
生成的项目：**统一 Handler 模板 + 原生 Gin 运行时 + fuego 仅作开发期 OpenAPI 文档生成器，release 构建纯净、排除开发期多余依赖**。

## 安装

```bash
cd scaffold
go build -o fuego-hinge.exe .          # 生成二进制
# 或安装到 GOBIN（全局可用）
go install .
```

## 用法

```bash
fuego-hinge create myapp                        # 生成 ./myapp，module 名 = myapp
fuego-hinge create myapp -m github.com/me/myapp # 自定义 module 路径
fuego-hinge create myapp --no-tidy              # 跳过依赖拉取
fuego-hinge create myapp --force                # 覆盖已存在目录
fuego-hinge create myapp -h                     # 帮助
```

flags 可放在项目名之前或之后（`-m` 支持空格分隔与 `=` 两种写法）。

## 生成的项目（模板）

```
myapp/
├── main.go                  # 运行时入口（原生 gin，//go:build !openapi）
├── main_doc.go              # 开发期文档生成入口（//go:build openapi）
├── build.sh                 # release(含 fuego 检查)/dev/spec/test
├── app/                     # ★ 业务层
│   ├── handlers/            # 统一 Handler: func(ctx, Q, B) (R, error) + 示例业务
│   └── routes/routes.go     # 路由注册表（唯一注册入口）
└── internal/                # 框架层
    ├── contract/            # 核心契约 RouteMeta[Q,B,R]
    ├── response/            # 统一响应壳 {code, data, msg}
    ├── server/              # Gin 适配器（绑定/校验/错误映射/中间件扩展点）
    ├── validator/           # 校验器
    └── openapi/             # 开发期文档生成（fuego 引擎，构建期隔离）
```

生成时会自动：
- 把所有 `fuego-hinge` 替换为你的 module 名（import 路径、README、go.mod）
- 把 `FUEGO_HINGE_ENV` 替换为 module 名推导的环境变量前缀（如 `MYAPP_ENV`）
- 执行 `go mod tidy` 拉取依赖

## 修改指南

- **模板内容**：改 `template/` 目录下的文件（改完重新 `go build` 即可，模板随二进制内置）
- **新增默认依赖**：改 `template/go.mod.tmpl`（生成项目继承）
- **新增模板文件**：放入 `template/` 子目录即可，自动随二进制分发
- **CLI 行为**：改 `main.go`（flags、替换逻辑、输出提示）

## 模板更新流程

框架（`../` 目录）迭代后，同步到 `template/`：

```powershell
# PowerShell 示例（按需增删文件）
$files = @("main.go","main_doc.go","build.sh","README.md",...)
foreach ($f in $files) { Copy-Item "..\$f" "template\$f" -Force }
# 注意：go.mod 需手动改名复制为 template/go.mod.tmpl
```

## 设计说明

- 单二进制分发：模板通过 `//go:embed template` 内置，无外部文件依赖
- 生成物可控：模板不包含 `go.sum`/`bin/`/`openapi.yaml`（生成后由 `go mod tidy` 与构建产生）
- 模板内 `go.mod` 以 `go.mod.tmpl` 存储，生成时改名为 `go.mod`（避免模板目录被 Go 视为嵌套模块）
- 占位替换基于框架统一 module 名 `fuego-hinge`（见 `template/go.mod.tmpl`），小写精确匹配，避免误伤
