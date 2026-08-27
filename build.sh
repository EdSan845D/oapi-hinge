#!/bin/bash
# opai-hinge 构建脚本
#   -r  release：默认标签构建，并自动校验依赖链
#   -d  dev：本机调试构建
#   -s  spec：生成 OpenAPI 文档（-tags openapi 独立构建）
#   -t  test：运行全部测试

usage() {
  echo "Usage: $0 [-r] release [-d] dev [-s] spec [-t] test" 1>&2
  exit 1
}

case "$1" in
-r)
  mkdir -p bin
  CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/app . || exit 1
  echo "--- release 依赖链检查---"
  if go list -deps . | grep -q openapi3; then
    echo "FAIL: release 构建包含 openapi3 import"
    exit 1
  fi
  echo "OK: release 构建"
  ;;
-d)
  mkdir -p bin
  go build -o bin/app-dev .
  ;;
-s)
  go run -tags openapi . -out openapi.yaml
  ;;
-t)
  go test ./...
  ;;
*)
  usage
  ;;
esac