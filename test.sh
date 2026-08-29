#!/usr/bin/env bash
# oapi-hinge 统一测试入口：全模块 build + vet + test（openapi 走 -tags openapi）
# 用法：./test.sh
set -e
cd "$(dirname "$0")"

for m in contract servergin serverecho example; do
  echo "=== $m ==="
  (cd "$m" && go build ./... && go vet ./... && go test ./...)
done

echo "=== openapi (-tags openapi) ==="
(cd openapi && go build -tags openapi ./... && go vet -tags openapi ./... && go test -tags openapi ./...)

echo "ALL PASS"
