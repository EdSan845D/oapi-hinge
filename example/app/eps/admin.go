package eps

import (
	"context"
	"strings"
	"time"

	"github.com/EdSan845D/oapi-hinge/gen"
)

// ============ Enterpoint：管理（oapi:parent 挂载链演示） ============
//
// 注册逻辑说明（挂载链与根挂载并存）：
//   - 产物仍是每个 owner 一个 Register{Owner}GinRow(i, k, ep)，i 为传入的根
//     router（main.go 里是 r.Group("/api")）；oapi:parent 挂载链只在生成期把
//     祖先链前缀烘焙进路由路径字符串，运行时没有嵌套 Group 结构。
//   - 本组两级挂载：/admin（AdminEp 自身前缀）+ /audit（AuditEp 的 oapi:parent
//     + 自身前缀）→ 最终路径 /api/admin/audit/events（/api 来自 main.go 的运行时根）。
//   - 中间件沿祖先链继承（先根后叶）：AdminEp 的组级 Auth（框架原生通道）
//     作用于全部后代；AuditEp 的 AccessLog（内核拦截器通道）只作用于审计端点。
//   - 树外的 Enterpoint（SystemEp / UserEp / FileEp）不受影响，照常根挂载——
//     挂载链与“默认全挂到根”两种形态可以自由并存。

// AdminEp 管理端点：挂载链的根，组根 Enterpoint（纯挂载层演示）。
//
// oapi:prefix /admin
// oapi:middleware middleware.Auth
// oapi:tag 管理
type AdminEp struct {
	gen.EntryPoint
}

// Index 组根路由：oapi:route 省路径即组根（/admin）。
//
// oapi:route GET
// 管理面板索引
func (ep AdminEp) Index(ctx context.Context) (map[string]string, error) {
	return map[string]string{
		"module":  "admin",
		"message": "管理面板索引（挂载链根）",
	}, nil
}

// AuditEp 审计端点：oapi:parent 声明挂到 AdminEp 下，自身前缀 /audit。
//
// oapi:parent AdminEp
// oapi:prefix /audit
// oapi:interceptor middleware.AccessLog
// oapi:tag 审计
type AuditEp struct{}

// AuditEvent 审计事件
type AuditEvent struct {
	// 事件ID
	ID string `json:"id"`
	// 动作
	Action string `json:"action"`
	// 发生时间
	At time.Time `json:"at"`
}

// AuditQ 审计事件列表入参
type AuditQ struct {
	// 动作过滤(子串匹配,可选)
	Action string `query:"action"`
	// 页码
	Page int `query:"page" default:"1"`
}

// oapi:route GET /events
// 审计事件列表(两级挂载演示:/admin(父挂载点)+ /audit(本节点挂载点)+ /events)
func (ep AuditEp) ListEvents(ctx context.Context, q AuditQ) (Paged[AuditEvent], error) {
	events := []AuditEvent{
		{ID: "e1", Action: "user.create", At: time.Now().Add(-2 * time.Hour)},
		{ID: "e2", Action: "user.delete", At: time.Now().Add(-1 * time.Hour)},
		{ID: "e3", Action: "role.grant", At: time.Now().Add(-30 * time.Minute)},
	}
	var filtered []AuditEvent
	for _, e := range events {
		if q.Action == "" || strings.Contains(e.Action, q.Action) {
			filtered = append(filtered, e)
		}
	}
	return Paged[AuditEvent]{Items: filtered, Total: int64(len(filtered))}, nil
}
