package servergin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/gin-gonic/gin"
)

// ============ ParamBinder：自定义类型绑定（逗号切片 / ID→缓存实体）============
// 其余升级能力测试见 server_test.go / feature_test.go。

type tagIDs []string

type binderUser struct {
	ID   string
	Name string
}

var binderUserCache = map[string]*binderUser{
	"42": {ID: "42", Name: "Answer"},
}

type binderReq struct {
	Tags tagIDs      `query:"tags"`
	User *binderUser `query:"uid"`
}

func binderHandler(ctx context.Context, req binderReq, _ any) (map[string]any, error) {
	return map[string]any{"tags": req.Tags, "user": req.User}, nil
}

func init() {
	contract.RegisterParamBinder(func(src []string) (tagIDs, error) {
		var out tagIDs
		for _, s := range src {
			out = append(out, strings.Split(s, ",")...)
		}
		return out, nil
	})
	contract.RegisterParamBinder(func(src []string) (*binderUser, error) {
		u, ok := binderUserCache[src[0]]
		if !ok {
			return nil, contract.NotFound("用户不存在: " + src[0])
		}
		return u, nil
	})
}

func TestParamBinder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	groups := []*contract.Group{
		{
			Prefix: "/b",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[binderReq, any, map[string]any]{
					Method: "GET", Path: "/query", Summary: "绑定器",
					Handler: binderHandler,
				}),
			},
		},
	}
	New().Mount(r.Group(""), groups)

	// 逗号串 → 命名切片；ID → 缓存实体
	w := call(t, r, http.MethodGet, "/b/query?tags=a,b,c&uid=42", "")
	if w.Code != http.StatusOK {
		t.Fatalf("binder = %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"tags":["a","b","c"]`) || !strings.Contains(body, `"ID":"42"`) {
		t.Fatalf("param binder not applied: %s", body)
	}

	// 重复参数 + 逗号混合
	w = call(t, r, http.MethodGet, "/b/query?tags=x,y&tags=z", "")
	if !strings.Contains(w.Body.String(), `"tags":["x","y","z"]`) {
		t.Fatalf("multi-value binder: %s", w.Body.String())
	}

	// binder 返回 StatusError → 404（绑定阶段错误走统一错误链）
	w = call(t, r, http.MethodGet, "/b/query?uid=99", "")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "用户不存在") {
		t.Fatalf("binder 404 = %d %s", w.Code, w.Body.String())
	}

	// 参数缺失 → 字段零值（tags nil、user nil）
	w = call(t, r, http.MethodGet, "/b/query", "")
	if !strings.Contains(w.Body.String(), `"tags":null`) || !strings.Contains(w.Body.String(), `"user":null`) {
		t.Fatalf("missing params = %s", w.Body.String())
	}
}
