package eps

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// ============ Enterpoint：系统（无前缀、无鉴权） ============

// SystemEp 系统端点。
type SystemEp struct{}

// oapi:route GET /health
// 健康检查
func (ep SystemEp) Health(ctx context.Context, _ any) (map[string]string, error) {
	return map[string]string{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	}, nil
}

// ============ Enterpoint：用户（组前缀 + 组鉴权） ============

// UserEp 用户端点。
//
// oapi:prefix /users
// oapi:tag 用户
// oapi:auth BearerAuth
type UserEp struct {
	Store *UserStore
}

// oapi:route GET
// 用户列表（分页）
func (ep UserEp) ListUsers(ctx context.Context, q ListUsersReq) (hinge.Paged[User], error) {
	items, total := ep.Store.Page(q.Page, q.Size)
	return hinge.Paged[User]{Items: items, Total: total}, nil
}

// oapi:route GET /{id}
// 用户详情
func (ep UserEp) GetUser(ctx context.Context, q GetUserReq) (User, error) {
	if u, ok := ep.Store.Get(q.ID); ok {
		return u, nil
	}
	return User{}, hinge.NotFound("用户不存在")
}

// oapi:route POST
// oapi:status 201
// 创建用户
func (ep UserEp) CreateUser(ctx context.Context, _ any, b CreateUserReq) (User, error) {
	return ep.Store.Create(b.Name, b.Email), nil
}

// oapi:route DELETE /{id}
// 删除用户（Empty 响应：data 为 null）
func (ep UserEp) DeleteUser(ctx context.Context, q DeleteUserReq) (hinge.Empty, error) {
	if !ep.Store.Delete(q.ID) {
		return nil, hinge.NotFound("用户不存在")
	}
	return nil, nil
}

// oapi:route PATCH /{id}/password
// 修改密码（出参脱敏演示：InTransform 规范化 + validate 标签 + OutTransform）
func (ep UserEp) ChangePassword(ctx context.Context, q ChangePasswordReq) (MaskedUser, error) {
	u, ok := ep.Store.Get(q.ID)
	if !ok {
		return MaskedUser{}, hinge.NotFound("用户不存在")
	}
	// 真实场景在此更新存储中的凭证哈希
	return MaskedUser{Name: u.Name, Email: u.Email}, nil
}

// ============ Enterpoint：文件 ============

// FileEp 文件端点。
//
// oapi:prefix /files
// oapi:tag 文件
type FileEp struct{}

// oapi:route GET /{name}
// 下载示例文件（FileStream 二进制流响应）
func (ep FileEp) DownloadSample(ctx context.Context, q DownloadSampleReq) (*hinge.FileStream, error) {
	name := strings.TrimSuffix(q.Name, "/")
	if name == "" || strings.Contains(name, "..") {
		return nil, hinge.BadRequest("invalid file name")
	}
	content := fmt.Sprintf("oapi-hinge sample file: %s\n生成时间: %s\n", name, time.Now().Format(time.RFC3339))
	return &hinge.FileStream{
		Name:        name,
		Size:        int64(len(content)),
		ContentType: "text/plain; charset=utf-8",
		Reader:      strings.NewReader(content),
	}, nil
}
