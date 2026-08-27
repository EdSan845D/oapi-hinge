package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/EdSan845D/oapi-hinge/contract/response"
)

// ============ 示例业务：用户管理（内存存储，开箱即跑） ============
// 删除本文件即可开始写自己的业务；参考它覆盖的四种典型形态：
//   - ListUsers   GET    无 body，query 分页参数
//   - GetUser     GET    路径参数 + ErrNotFound
//   - CreateUser  POST   JSON body + 标签必填 + Validate() 自定义校验
//   - DeleteUser  DELETE 路径参数 + Empty 响应（data: null）

type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	usersMu sync.RWMutex
	users   = []User{
		{ID: "u1", Name: "Alice", Email: "alice@example.com", CreatedAt: time.Now().Add(-48 * time.Hour)},
		{ID: "u2", Name: "Bob", Email: "bob@example.com", CreatedAt: time.Now().Add(-24 * time.Hour)},
	}
)

// ListUsersReq 用户列表（分页）
type ListUsersReq struct {
	Page int `query:"page" default:"1" description:"页码"`
	Size int `query:"size" default:"10" description:"每页条数"`
}

// ListUsers 用户列表
func ListUsers(ctx context.Context, req ListUsersReq, _ any) (response.Paged[User], error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Size <= 0 {
		req.Size = 10
	}
	usersMu.RLock()
	defer usersMu.RUnlock()
	total := int64(len(users))
	start := (req.Page - 1) * req.Size
	if start >= len(users) {
		return response.Paged[User]{Items: []User{}, Total: total}, nil
	}
	end := start + req.Size
	if end > len(users) {
		end = len(users)
	}
	return response.Paged[User]{Items: users[start:end], Total: total}, nil
}

// GetUserReq 用户详情
type GetUserReq struct {
	ID string `path:"id" description:"用户ID"`
}

// GetUser 用户详情
func GetUser(ctx context.Context, req GetUserReq, _ any) (User, error) {
	usersMu.RLock()
	defer usersMu.RUnlock()
	for _, u := range users {
		if u.ID == req.ID {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

// CreateUserReq 创建用户（演示：标签必填 + Validate() 自定义校验）
type CreateUserReq struct {
	Name  string `json:"name" binding:"required" description:"姓名"`
	Email string `json:"email" binding:"required" description:"邮箱"`
}

// InTransform 入参规范化（升级能力）：适配器在绑定后、校验前自动调用；
// trim 后 " Cara " 也能通过 required 必填检查
func (r *CreateUserReq) InTransform(ctx context.Context) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Email = strings.ToLower(r.Email)
	return nil
}

// Validate 自定义校验：运行时适配器在校验阶段自动调用
func (r CreateUserReq) Validate() error {
	if !strings.Contains(r.Email, "@") {
		return errors.New("invalid email: must contain @")
	}
	return nil
}

// CreateUser 创建用户
func CreateUser(ctx context.Context, _ NoReq, req CreateUserReq) (User, error) {
	usersMu.Lock()
	defer usersMu.Unlock()
	u := User{
		ID:        fmt.Sprintf("u%d", len(users)+1),
		Name:      req.Name,
		Email:     req.Email,
		CreatedAt: time.Now(),
	}
	users = append(users, u)
	return u, nil
}

// DeleteUserReq 删除用户
type DeleteUserReq struct {
	ID string `path:"id" description:"用户ID"`
}

// DeleteUser 删除用户（Empty 响应：data 为 null）
func DeleteUser(ctx context.Context, req DeleteUserReq, _ any) (Empty, error) {
	usersMu.Lock()
	defer usersMu.Unlock()
	for i, u := range users {
		if u.ID == req.ID {
			users = append(users[:i], users[i+1:]...)
			return nil, nil
		}
	}
	return nil, contract.NotFound("用户不存在")
}

// Health 健康检查（无鉴权示例）
func Health(ctx context.Context, _ NoReq, _ any) (map[string]string, error) {
	return map[string]string{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	}, nil
}

// ============ 升级能力演示：修改密码 ============
// 演示组合：InTransform 规范化入参 + validate 标签（配合 Playground 校验器）
// + OutTransform 出参脱敏 + StatusError 404

// ChangePasswordReq 修改密码
type ChangePasswordReq struct {
	ID       string `path:"id" description:"用户ID"`
	Password string `json:"password" validate:"required,min=8" description:"新密码"`
}

// InTransform 绑定后自动执行：去除首尾空白
func (r *ChangePasswordReq) InTransform(ctx context.Context) error {
	r.Password = strings.TrimSpace(r.Password)
	return nil
}

// MaskedUser 脱敏后的用户信息
type MaskedUser struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// OutTransform 序列化前自动执行：邮箱脱敏 alice@example.com -> a***@example.com
func (u *MaskedUser) OutTransform(ctx context.Context) error {
	if at := strings.Index(u.Email, "@"); at > 1 {
		u.Email = u.Email[:1] + "***" + u.Email[at:]
	}
	return nil
}

// ChangePassword 修改密码（业务层零框架依赖：真实场景在此更新存储中的凭证哈希）
func ChangePassword(ctx context.Context, req ChangePasswordReq, _ any) (MaskedUser, error) {
	usersMu.RLock()
	defer usersMu.RUnlock()
	for _, u := range users {
		if u.ID == req.ID {
			return MaskedUser{Name: u.Name, Email: u.Email}, nil
		}
	}
	return MaskedUser{}, contract.NotFound("用户不存在")
}
