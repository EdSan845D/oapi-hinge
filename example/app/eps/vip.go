package eps

import (
	"context"
)

// ============ Enterpoint：VIP 用户（端点集提升 / struct 嵌入演示） ============
//
// 嵌入 UserEp：Go 方法提升语义下，UserEp 的全部端点方法成为 VipUserEp 的
// 有效方法集，gen 以 VipUserEp 为 owner 克隆发射（端点集提升）——
//   - 路径：VipUserEp 的 oapi:prefix /users/vip + 原方法 rel
//     → /api/users/vip（List 提升）、/api/users/vip/{id}（Get 提升）、/api/users/vip/panel
//   - 方法级注解随方法走；struct 级注解不随：VipUserEp 用自己的
//     oapi:middleware Auth / oapi:tag，与 UserEp（/users，独立照旧）身份解耦
//   - Store 字段随 UserEp 提升：装配时给 VipUserEp.UserEp.Store 赋值
//     （main.go 与 UserEp 共享同一 store 实例）

// VipUserEp VIP 用户端点：嵌入 UserEp 复用其端点集。
//
// oapi:prefix /users/vip
// oapi:middleware middleware.Auth
// oapi:tag VIP用户
type VipUserEp struct {
	UserEp
	Level int
}

// oapi:route GET /panel
// VIP 面板
func (ep VipUserEp) LevelContent(ctx context.Context, _ any) (map[string]string, error) {
	return map[string]string{
		"module":  "vip",
		"message": "wellcome, my dear vip",
	}, nil
}
