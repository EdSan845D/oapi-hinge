#!/usr/bin/env bash
# oapi-hinge 统一测试入口：全仓 build + vet + test（openapi 包测试默认覆盖）
# 用法：./test.sh
set -e
cd "$(dirname "$0")"

echo "=== 根模块（内核 / 生成器 / 文档）：零框架依赖 ==="
go build ./...
go vet ./...
go test ./...

echo "=== 适配器子模块（gin / echo / http / validator） ==="
(cd servergin  && go build ./... && go vet ./... && go test ./...)
(cd serverecho && go build ./... && go vet ./... && go test ./...)
(cd serverhttp && go build ./... && go vet ./... && go test ./...)
(cd validator  && go build ./... && go vet ./...)

echo "ALL PASS"
