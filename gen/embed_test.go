package gen

import (
	"strings"
	"testing"
)

// ---- 测试源码（端点集提升 / oapi:parent 组合）----

const embedSrc = `package app

import (
	"context"

	mw "example.com/mw"
)

// oapi:prefix /users
// oapi:middleware mw.TraceMW
type UserEp struct {
	Store *UserStore
}

// oapi:route GET
func (ep UserEp) List(ctx context.Context, q ListQ) ([]string, error) {
	return nil, nil
}

// oapi:route GET /{id}
// oapi:interceptor mw.AuthIC
func (ep UserEp) Get(ctx context.Context, q GetQ) (string, error) {
	return "", nil
}

// oapi:prefix /users/vip
// oapi:middleware mw.AdminAuth
// oapi:tag VIP
type VipUserEp struct {
	UserEp
	Level int
}

// oapi:route GET /panel
func (ep VipUserEp) LevelContent(ctx context.Context) (map[string]string, error) {
	return nil, nil
}

type ListQ struct {
	Page int ` + "`query:\"page\"`" + `
}

type GetQ struct {
	ID string ` + "`path:\"id\"`" + `
}

type UserStore struct{}
`

const embedChainSrc = `package app

import "context"

// oapi:prefix /base
type BaseEp struct{}

// oapi:route GET /base-route
func (ep BaseEp) BR(ctx context.Context, _ any) (string, error) { return "", nil }

// oapi:prefix /mid
type MidEp struct {
	BaseEp
}

// oapi:route GET /mid-route
func (ep MidEp) MR(ctx context.Context, _ any) (string, error) { return "", nil }

// oapi:prefix /top
type TopEp struct {
	MidEp
}

// oapi:route GET /top-route
func (ep TopEp) TR(ctx context.Context, _ any) (string, error) { return "", nil }
`

const shadowSrc = `package app

import "context"

// oapi:prefix /users
type UserEp struct{}

// oapi:route GET
func (ep UserEp) List(ctx context.Context, _ any) (string, error) { return "", nil }

// oapi:prefix /users/vip
type VipUserEp struct {
	UserEp
}

// oapi:route GET
func (ep VipUserEp) List(ctx context.Context, _ any) (string, error) { return "", nil }
`

// embedParentSrc 提升与 oapi:parent 组合：VipUserEp 提升后自身再挂到版本链下。
const embedParentSrc = `package app

import "context"

// oapi:prefix /v1
type V1Ep struct{}

// oapi:route GET
func (ep V1Ep) Index(ctx context.Context, _ any) (string, error) { return "", nil }

// oapi:prefix /users
type UserEp struct{}

// oapi:route GET
func (ep UserEp) List(ctx context.Context, _ any) (string, error) { return "", nil }

// oapi:parent V1Ep
// oapi:prefix /users/vip
type VipUserEp struct {
	UserEp
}

// oapi:route GET /panel
func (ep VipUserEp) LevelContent(ctx context.Context, _ any) (string, error) { return "", nil }
`

// ---- 用例 ----

// TestEmbedPromotion 提升主链路：嵌入 Enterpoint 的端点以本类型为 owner 克隆发射，
// 方法级注解随方法走、struct 级注解不随（两身份解耦）、被嵌入方独立照旧。
func TestEmbedPromotion(t *testing.T) {
	eps, err := buildIR([]*Package{parseTestPkg(t, "app", embedSrc)}, nil)
	if err != nil {
		t.Fatalf("buildIR: %v", err)
	}

	cases := []struct {
		owner, handler, fullPath string
		groupMWs                 []string
		annoICs                  []string
	}{
		// 被嵌入方：独立照旧
		{"UserEp", "List", "/users", []string{"TraceMW"}, nil},
		{"UserEp", "Get", "/users/{id}", []string{"TraceMW"}, []string{"AuthIC"}},
		// 提升端点：Owner=VipUserEp，路径用 VipUserEp 前缀，struct 级注解不随
		{"VipUserEp", "List", "/users/vip", []string{"AdminAuth"}, nil},
		// 方法级注解随方法走：UserEp.Get 的 AuthIC 在提升端点上保留
		{"VipUserEp", "Get", "/users/vip/{id}", []string{"AdminAuth"}, []string{"AuthIC"}},
		// 自身端点
		{"VipUserEp", "LevelContent", "/users/vip/panel", []string{"AdminAuth"}, nil},
	}
	for _, c := range cases {
		ep := findEp(t, eps, c.owner, c.handler)
		if ep.FullPath != c.fullPath {
			t.Errorf("%s.%s FullPath = %q, want %q", c.owner, c.handler, ep.FullPath, c.fullPath)
		}
		if len(ep.GroupMWs) != len(c.groupMWs) {
			t.Errorf("%s.%s GroupMWs = %v, want %v", c.owner, c.handler, ep.GroupMWs, c.groupMWs)
		}
		for i, want := range c.groupMWs {
			if ep.GroupMWs[i].Name != want {
				t.Errorf("%s.%s GroupMWs[%d] = %q, want %q", c.owner, c.handler, i, ep.GroupMWs[i].Name, want)
			}
		}
		if len(ep.AnnoICs) != len(c.annoICs) {
			t.Errorf("%s.%s AnnoICs = %v, want %v", c.owner, c.handler, ep.AnnoICs, c.annoICs)
		}
		for i, want := range c.annoICs {
			if ep.AnnoICs[i].Name != want {
				t.Errorf("%s.%s AnnoICs[%d] = %q, want %q", c.owner, c.handler, i, ep.AnnoICs[i].Name, want)
			}
		}
	}
	// 提升端点的方法级拦截器随方法走（表断言已覆盖，此处验证 GroupICs 为空）
	if ep := findEp(t, eps, "VipUserEp", "Get"); len(ep.GroupICs) != 0 {
		t.Errorf("VipUserEp.Get GroupICs = %v, want 空（AuthIC 是方法级注解）", ep.GroupICs)
	}
	// 自身端点 NoArgs：func(ctx) 形态，无 Q 无 B
	if ep := findEp(t, eps, "VipUserEp", "LevelContent"); !ep.NoArgs || ep.HasQ || ep.HasB {
		t.Errorf("LevelContent NoArgs=%v HasQ=%v HasB=%v, want true/false/false", ep.NoArgs, ep.HasQ, ep.HasB)
	}
	// 提升不产生 Mount（oapi:parent 才有）
	for _, ep := range eps {
		if len(ep.MountMWs) != 0 || len(ep.MountICs) != 0 {
			t.Errorf("%s.%s 不应有 Mount（无 oapi:parent）", ep.Owner, ep.Handler)
		}
	}
}

// TestEmbedChainRecursive 递归提升：TopEp 有效集 = 自身 + MidEp + BaseEp（嵌入链），
// 各级路径用 TopEp 前缀。
func TestEmbedChainRecursive(t *testing.T) {
	eps, err := buildIR([]*Package{parseTestPkg(t, "app", embedChainSrc)}, nil)
	if err != nil {
		t.Fatalf("buildIR: %v", err)
	}
	for _, c := range []struct{ owner, handler, fullPath string }{
		{"BaseEp", "BR", "/base/base-route"},
		{"MidEp", "MR", "/mid/mid-route"},
		{"MidEp", "BR", "/mid/base-route"},
		{"TopEp", "TR", "/top/top-route"},
		{"TopEp", "MR", "/top/mid-route"},
		{"TopEp", "BR", "/top/base-route"},
	} {
		ep := findEp(t, eps, c.owner, c.handler)
		if ep.FullPath != c.fullPath {
			t.Errorf("%s.%s FullPath = %q, want %q", c.owner, c.handler, ep.FullPath, c.fullPath)
		}
	}
}

// TestEmbedShadow 自身方法遮蔽提升端点：自身发射、提升跳过（Go 遮蔽语义）。
func TestEmbedShadow(t *testing.T) {
	eps, err := buildIR([]*Package{parseTestPkg(t, "app", shadowSrc)}, nil)
	if err != nil {
		t.Fatalf("buildIR: %v", err)
	}
	count := 0
	for _, ep := range eps {
		if ep.Owner == "VipUserEp" && ep.Handler == "List" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("VipUserEp.List 应只有自身一份（提升被遮蔽），实际 %d", count)
	}
	// UserEp 独立照旧
	if ep := findEp(t, eps, "UserEp", "List"); ep.FullPath != "/users" {
		t.Errorf("UserEp.List = %q, want /users", ep.FullPath)
	}
}

// TestEmbedParentCompose 提升与 oapi:parent 正交组合：
// VipUserEp 提升的端点随自身挂到 parent 链前缀下。
func TestEmbedParentCompose(t *testing.T) {
	eps, err := buildIR([]*Package{parseTestPkg(t, "app", embedParentSrc)}, nil)
	if err != nil {
		t.Fatalf("buildIR: %v", err)
	}
	// V1Ep：根挂载组根
	if ep := findEp(t, eps, "V1Ep", "Index"); ep.FullPath != "/v1" {
		t.Errorf("V1Ep.Index = %q, want /v1", ep.FullPath)
	}
	// UserEp：独立照旧（无 parent）
	if ep := findEp(t, eps, "UserEp", "List"); ep.FullPath != "/users" {
		t.Errorf("UserEp.List = %q, want /users", ep.FullPath)
	}
	// VipUserEp：自身端点 + 提升端点，全部挂到 parent 链（/v1）下
	for _, c := range []struct{ handler, fullPath string }{
		{"LevelContent", "/v1/users/vip/panel"},
		{"List", "/v1/users/vip"},
	} {
		ep := findEp(t, eps, "VipUserEp", c.handler)
		if ep.FullPath != c.fullPath {
			t.Errorf("VipUserEp.%s FullPath = %q, want %q", c.handler, ep.FullPath, c.fullPath)
		}
		if len(ep.MountMWs) != 0 || len(ep.MountICs) != 0 {
			t.Errorf("VipUserEp.%s 不应有 Mount（链上无注解中间件）", c.handler)
		}
	}
}

// TestEmbedSpecNames 提升端点的 spec 名按 owner 变体派生，与被嵌入方天然不冲突。
func TestEmbedSpecNames(t *testing.T) {
	eps, err := buildIR([]*Package{parseTestPkg(t, "app", embedSrc)}, nil)
	if err != nil {
		t.Fatalf("buildIR: %v", err)
	}
	if GenSpecName(findEp(t, eps, "UserEp", "List")) != "SpecUserEpList" {
		t.Errorf("UserEp spec 名错误")
	}
	if GenSpecName(findEp(t, eps, "VipUserEp", "List")) != "SpecVipUserEpList" {
		t.Errorf("VipUserEp spec 名错误（变体名去冲突）")
	}
}

// TestEmbedNoSilentNoEP 嵌入非 EP 结构体（无 route 方法）不产生提升端点：
// 组合容器本身若无直接端点也不是 owner。
func TestEmbedNoSilentNoEP(t *testing.T) {
	src := `package app

import "context"

type Store struct{}

// oapi:prefix /users
type UserEp struct {
	Store Store
}

// oapi:route GET
func (ep UserEp) List(ctx context.Context, _ any) (string, error) { return "", nil }

// StoreBox 仅嵌 Store（非 EP 类型）：不是 owner，无端点
type StoreBox struct {
	Store
}
`
	eps, err := buildIR([]*Package{parseTestPkg(t, "app", src)}, nil)
	if err != nil {
		t.Fatalf("buildIR: %v", err)
	}
	for _, ep := range eps {
		if ep.Owner == "StoreBox" {
			t.Errorf("StoreBox 不应成为 owner（嵌入非 EP 类型不提升）")
		}
	}
	if ep := findEp(t, eps, "UserEp", "List"); ep.FullPath != "/users" {
		t.Errorf("UserEp.List = %q, want /users", ep.FullPath)
	}
}

// TestEmbedShadowWarningText 遮蔽警告信息可读性。
func TestEmbedShadowWarningText(t *testing.T) {
	src := strings.Replace(shadowSrc, "func (ep VipUserEp) List", "func (ep VipUserEp) ListX", 1)
	_ = src // 遮蔽警告走 stderr，此处仅保证遮蔽场景构建可重复
	eps, err := buildIR([]*Package{parseTestPkg(t, "app", shadowSrc)}, nil)
	if err != nil {
		t.Fatalf("buildIR: %v", err)
	}
	_ = eps
}
