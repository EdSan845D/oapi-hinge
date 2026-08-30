#!/bin/bash
# release 构建：静态编译（不含文档生成器，-tags openapi 独立构建）
#   ./build.sh -r   release 构建（bin/app）
#   ./build.sh -s   生成 OpenAPI 文档（openapi.yaml）

case "$1" in
-r)
  mkdir -p bin
  CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/app . || exit 1
  echo "release build -> bin/app"
  ;;
-s)
  go run -tags openapi . -out openapi.yaml
  ;;
*)
  echo "Usage: $0 [-r] release [-s] spec" 1>&2
  exit 1
  ;;
esac
