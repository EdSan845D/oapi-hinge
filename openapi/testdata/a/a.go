// Package a 两阶段命名冲突测试用：与 b 包同名类型/函数（仅 -tags openapi 测试引用）。
// 同时作为「注释即文档」的注释样例。
package a

import (
	"context"

	"github.com/EdSan845D/oapi-hinge/contract"
)

// A 包用户（注释即文档样例）
type User struct {
	// 用户ID，全局唯一
	ID string `json:"id"`
	// 用户名
	Name string `json:"name"`
	// 邮箱（注释不应出现，标签优先）
	Email string `json:"email" description:"邮箱(标签优先)"`
}

// A 包健康检查
// 返回 A 包用户信息
func Health(ctx context.Context, _ contract.NoReq, _ any) (User, error) {
	return User{ID: "a1", Name: "from-a"}, nil
}
