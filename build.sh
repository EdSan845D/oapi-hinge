#!/bin/bash
# opai-hinge 构建脚本（框架仓库：目标是 example 演示应用）
#   -r  release：构建 bin/app，并校验 release 依赖链（不得包含文档生成器）
#   -d  dev：本机调试构建
#   -s  spec：生成 example 的 OpenAPI 文档（docs/ 独立入口）
#   -t  test：运行全部测试

usage() {
  echo "Usage: $0 [-r] release [-d] dev [-s] spec [-t] test" 1>&2
  exit 1
}

case "$1" in
-r)
  mkdir -p bin
  (cd example && CGO_ENABLED=0 go build -ldflags "-s -w" -o ../bin/app .) || exit 1
  echo "--- release 依赖链检查---"
  if (cd example && go list -deps . | grep -q "oapi-hinge/openapi"); then
    echo "FAIL: release 构建包含文档生成器（oapi-hinge/openapi）"
    exit 1
  fi
  echo "OK: release 构建"
  ;;
-d)
  mkdir -p bin
  (cd example && go build -o ../bin/app-dev .)
  ;;
-s)
  (cd example && go run ./docs -out openapi.yaml)
  ;;
-t)
  go test ./...
  ;;
*)
  usage
  ;;
esac
