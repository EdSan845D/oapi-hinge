#!/usr/bin/env bash
# oapi-hinge 多模块锁步发布脚本
#
# 用法：./release.sh vX.Y.Z [--skip-tests]
#
# 动作（全模块同一提交锁步发布）：
#   1. 校验版本号格式与工作区干净
#   2. （可选跳过）跑全量测试（test.sh：根模块 + 适配器子模块 + example）
#   3. 子模块 go.mod 的内核依赖版本对齐到本次发布版本
#   4. 提交对齐变更（如有）
#   5. 全模块打 tag：根 vX.Y.Z + 子模块 <dir>/vX.Y.Z
#   6. 推送分支与全部 tag
#
# 提醒：国内 GOPROXY（goproxy.cn 等）同步新 tag 可能有几分钟延迟；
# 消费者立即 go get 报 404 时，可 GOPROXY=direct 重试或稍后再取。
set -euo pipefail
cd "$(dirname "$0")"

VER="${1:-}"
[[ "$VER" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "用法: ./release.sh vX.Y.Z"; exit 1; }

SKIP_TESTS=0
[[ "${2:-}" == "--skip-tests" ]] && SKIP_TESTS=1

SUBMODULES=(servergin serverecho serverhttp validator)
CORE=github.com/EdSan845D/oapi-hinge

# 0. 工作区必须干净：tag 指向的提交就是发布内容
[[ -z "$(git status --porcelain)" ]] || {
  echo "FAIL: 工作区不干净（有未提交/未暂存改动），先提交或暂存"
  git status --short
  exit 1
}

# 1. 全量测试（根模块 + 适配器子模块 + example）
if [[ "$SKIP_TESTS" -ne 1 ]]; then
  ./test.sh
fi

# 2. 子模块 go.mod 内核依赖对齐到本次发布版本
#（首个发布版本通常已对齐，此处为幂等操作；工作区构建不受该版本影响）
for m in "${SUBMODULES[@]}"; do
  (cd "$m" && go mod edit -require=$CORE@$VER)
done

# 3. 对齐变更如有则提交
git add servergin/go.mod serverecho/go.mod serverhttp/go.mod validator/go.mod
if ! git diff --cached --quiet; then
  git commit -m "release $VER：子模块内核依赖对齐"
fi

# 4. 全模块打 tag（同一提交）
git tag -f "$VER"
git tag -f servergin/$VER
git tag -f serverecho/$VER
git tag -f serverhttp/$VER
git tag -f validator/$VER

# 5. 推送当前分支与全部 tag（显式列出，不用 --tags 以免带上陈旧 tag）
BRANCH=$(git branch --show-current)
git push origin "refs/heads/$BRANCH" \
  "refs/tags/$VER" \
  "refs/tags/servergin/$VER" \
  "refs/tags/serverecho/$VER" \
  "refs/tags/serverhttp/$VER" \
  "refs/tags/validator/$VER"

echo
echo "=== 发布完成：$VER ==="
echo "tag 清单：$VER servergin/$VER serverecho/$VER serverhttp/$VER validator/$VER"
echo "GOPROXY 同步可能有几分钟延迟；消费者 404 时可 GOPROXY=direct 重试。"
