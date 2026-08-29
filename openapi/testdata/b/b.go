// Package b 两阶段命名冲突测试用：与 a 包同名类型/函数（仅 -tags openapi 测试引用）。
package b

import (
	"context"

	"github.com/EdSan845D/oapi-hinge/contract"
)

// User 与 a.User 同名：探测裸名 "User" 冲突，应升级为 "b_User"
type User struct {
	ID string `json:"id"`
	// 用户名 | 示例:alice
	Name  string `json:"name"`
	Title string `json:"title"`
}

// Health 与 a.Health 同名：operationID 裸名 "Health" 冲突
func Health(ctx context.Context, _ contract.NoReq, _ any) (User, error) {
	return User{ID: "b1", Title: "from-b"}, nil
}
