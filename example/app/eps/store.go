// Package eps 业务端点（Enterpoint）：oapi:* 注解 + 端点方法 = 全部路由声明。
//
// v0.2 范式：本包没有路由注册代码——
//   - 路由注册：hinge gen 生成的 apigen 包（RegisterAllGin / Echo / HTTP）
//   - 路径↔函数对应表：本包 hinge_gen_table.go（生成）
//   - OpenAPI 文档：main_doc.go 消费 Endpoints() 表
//
// 端点方法是普通函数：单元测试直接调用，无需启动 HTTP 服务器。
package eps

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// User 用户
type User struct {
	// 用户ID，全局唯一
	ID string `json:"id"`
	// 用户名
	Name string `json:"name"`
	// 邮箱，用于登录与通知
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// UserStore 内存用户存储（示例实现；真实项目替换为数据库）。
type UserStore struct {
	mu    sync.RWMutex
	users []User
}

// NewUserStore 创建带种子数据的存储。
func NewUserStore() *UserStore {
	return &UserStore{users: []User{
		{ID: "u1", Name: "Alice", Email: "alice@example.com", CreatedAt: time.Now().Add(-48 * time.Hour)},
		{ID: "u2", Name: "Bob", Email: "bob@example.com", CreatedAt: time.Now().Add(-24 * time.Hour)},
	}}
}

// Page 分页查询
func (s *UserStore) Page(page, size int) (items []User, total int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 10
	}
	total = int64(len(s.users))
	start := (page - 1) * size
	if start >= len(s.users) {
		return []User{}, total
	}
	end := start + size
	if end > len(s.users) {
		end = len(s.users)
	}
	return s.users[start:end], total
}

// Get 按 ID 查询
func (s *UserStore) Get(id string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.ID == id {
			return u, true
		}
	}
	return User{}, false
}

// Create 创建
func (s *UserStore) Create(name, email string) User {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := User{ID: "u" + time.Now().Format("150405.000000000"), Name: name, Email: email, CreatedAt: time.Now()}
	s.users = append(s.users, u)
	return u
}

// Delete 删除；返回是否存在
func (s *UserStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.users {
		if u.ID == id {
			s.users = append(s.users[:i], s.users[i+1:]...)
			return true
		}
	}
	return false
}

// ---- 入参 / 出参类型（Q/B/R：签名即契约）----

// ListUsersReq 用户列表（分页）
type ListUsersReq struct {
	// 页码
	Page int `query:"page" default:"1"`
	// 每页条数
	Size int `query:"size" default:"10"`
}

// GetUserReq 用户详情
type GetUserReq struct {
	// 用户ID
	ID string `path:"id"`
}

// DeleteUserReq 删除用户
type DeleteUserReq struct {
	// 用户ID
	ID string `path:"id"`
}

// CreateUserReq 创建用户（演示：标签必填 + Validate() 自定义校验）
type CreateUserReq struct {
	// 姓名
	Name string `json:"name" binding:"required"`
	// 邮箱
	Email string `json:"email" binding:"required"`
}

// InTransform 入参规范化（生成绑定器在绑定后自动调用）：
// trim 后 " Cara " 也能通过 required 必填检查
func (r *CreateUserReq) InTransform(ctx context.Context) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Email = strings.ToLower(r.Email)
	return nil
}

// Validate 自定义校验（生成绑定器在必填检查后自动调用）
func (r CreateUserReq) Validate() error {
	if !strings.Contains(r.Email, "@") {
		return errors.New("invalid email: must contain @")
	}
	return nil
}

// ChangePasswordReq 修改密码
type ChangePasswordReq struct {
	// 用户ID
	ID string `path:"id"`
	// 新密码
	Password string `query:"password" validate:"required,min=8"`
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

// DownloadSampleReq 文件下载
type DownloadSampleReq struct {
	// 文件名
	Name string `path:"name"`
}
