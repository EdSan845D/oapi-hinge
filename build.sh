#!/bin/bash
# opai-hinge 构建脚本（框架仓库：目标是 example 演示应用）
#   -r  release：构建 bin/app，并校验 release 依赖链（不得包含文档生成器）
#   -d  dev：本机调试构建
#   -s  spec：生成 example 的 OpenAPI 文档（-tags openapi 独立构建）
#   -t  test：运行全部测试

usage() {
  echo "Usage: $0 [-r] release [-d] dev [-s] spec [-t] test" 1>&2
  exit 1
}

case "$1" in
-r)
  mkdir -p bin
  CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/app ./example || exit 1
  echo "--- release 依赖链检查---"
  if go list -deps ./example | grep -q "oapi-hinge/openapi"; then
    echo "FAIL: release 构建包含文档生成器（oapi-hinge/openapi）"
    exit 1
  fi
  echo "OK: release 构建"
  ;;
-d)
  mkdir -p bin
  go build -o bin/app-dev ./example
  ;;
-s)
  (cd example && go run -tags openapi . -out openapi.yaml)
  ;;
-t)
  go test ./... && go test -tags openapi ./...
  ;;
*)
  usage
  ;;
esac
