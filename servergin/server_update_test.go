package servergin

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"
	"github.com/gin-gonic/gin"
)

type userKey struct{}

// WithUser 将当前用户注入上下文（由运行时适配器的 ContextDecorator 调用）
func WithUser(ctx context.Context, user any) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// ErrNoUser 当前用户未注入
var ErrNoUser = errors.New("current user not found")

// CurrentUser 从上下文取出当前用户
func CurrentUser(ctx context.Context) (any, error) {
	user := ctx.Value(userKey{})
	if user == nil {
		return nil, ErrNoUser
	}
	return user, nil
}

// ---- decorate 前移：TransformIn / 校验器 / TransformOut 共享已装饰 ctx ----

type ctxUser struct{ ID string }

type ctxAwareReq struct {
	Name string `json:"name" binding:"required"`
}

func (r *ctxAwareReq) InTransform(ctx context.Context) error {
	// 前移后 InTransform 能读到 decorate 注入的用户
	u, err := CurrentUser(ctx)
	if err != nil {
		return err
	}
	r.Name = r.Name + ":" + u.(ctxUser).ID
	return nil
}

type ctxAwareOut struct {
	Name string `json:"name"`
}

func (o *ctxAwareOut) OutTransform(ctx context.Context) error {
	u, err := CurrentUser(ctx)
	if err != nil {
		return err
	}
	o.Name = o.Name + ":" + u.(ctxUser).ID
	return nil
}

func ctxAwareHandler(ctx context.Context, _ contract.NoReq, req ctxAwareReq) (ctxAwareOut, error) {
	return ctxAwareOut{Name: req.Name}, nil
}

func TestDecoratedCtxReachesAllPoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s := New()
	s.SetContextDecorator(func(c *gin.Context, ctx context.Context) context.Context {
		return WithUser(ctx, ctxUser{ID: "u42"})
	})
	var validatorSawUser bool
	s.AddValidator(func(ctx context.Context, method string, q, b any) error {
		_, err := CurrentUser(ctx)
		validatorSawUser = err == nil
		return nil
	})
	groups := []*contract.Group{
		{
			Prefix: "/c",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[contract.NoReq, ctxAwareReq, ctxAwareOut]{
					Method: "POST", Path: "/users", Summary: "ctx aware",
					Handler: ctxAwareHandler,
				}),
			},
		},
	}
	s.Mount(r.Group(""), groups)

	w := call(t, r, http.MethodPost, "/c/users", `{"name":"Alice"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("ctx aware = %d %s", w.Code, w.Body.String())
	}
	// InTransform 追加 :u42 → handler 收到 Alice:u42 → OutTransform 再追加 :u42
	if !strings.Contains(w.Body.String(), `"name":"Alice:u42:u42"`) {
		t.Fatalf("decorated ctx not visible in transforms: %s", w.Body.String())
	}
	if !validatorSawUser {
		t.Fatal("custom validator cannot see decorated ctx")
	}
}

// ============ ParamBinder：自定义类型绑定（逗号切片 / ID→缓存实体）============
// 其余升级能力测试见 server_base_test.go / server_feature_test.go / server_update_test.go。

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
	if b, ok := contract.BinderFor(reflect.TypeOf(tagIDs{})); ok {
		t.Logf("binder found ok")
		_, e2 := b([]string{"1,2"})
		t.Logf("binder call err: %v", e2)
	} else {
		t.Logf("binder NOT found for tagIDs")
	}
	if _, ok2 := contract.BinderFor(reflect.TypeOf(binderReq{}.Tags)); ok2 {
		t.Logf("field type binder found ok")
	} else {
		t.Logf("field type binder NOT found, type=%T", binderReq{}.Tags)
	}
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
