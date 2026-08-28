package serverecho

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"

	"github.com/labstack/echo/v4"
)

// ============ ParamBinder（echo 版）：逗号切片 / ID→缓存实体 ============

type echoTagIDs []string

type echoBinderUser struct {
	ID   string
	Name string
}

var echoBinderUserCache = map[string]*echoBinderUser{
	"42": {ID: "42", Name: "Answer"},
}

type echoBinderReq struct {
	Tags echoTagIDs      `query:"tags"`
	User *echoBinderUser `query:"uid"`
}

func echoBinderHandler(ctx context.Context, req echoBinderReq, _ any) (map[string]any, error) {
	return map[string]any{"tags": req.Tags, "user": req.User}, nil
}

func init() {
	contract.RegisterParamBinder(func(src []string) (echoTagIDs, error) {
		var out echoTagIDs
		for _, s := range src {
			out = append(out, strings.Split(s, ",")...)
		}
		return out, nil
	})
	contract.RegisterParamBinder(func(src []string) (*echoBinderUser, error) {
		u, ok := echoBinderUserCache[src[0]]
		if !ok {
			return nil, contract.NotFound("用户不存在: " + src[0])
		}
		return u, nil
	})
}

func TestEchoParamBinder(t *testing.T) {
	e := echo.New()
	groups := []*contract.Group{
		{
			Prefix: "/api",
			Routes: []contract.Route{
				contract.New(contract.RouteMeta[echoBinderReq, any, map[string]any]{
					Method: "GET", Path: "/binder", Summary: "绑定器",
					Handler: echoBinderHandler,
				}),
			},
		},
	}
	New().Mount(e.Group(""), groups)

	req := httptest.NewRequest(http.MethodGet, "/api/binder?tags=a,b&uid=42", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tags":["a","b"]`) || !strings.Contains(rec.Body.String(), `"ID":"42"`) {
		t.Fatalf("binder = %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/binder?uid=99", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "用户不存在") {
		t.Fatalf("binder 404 = %d %s", rec.Code, rec.Body.String())
	}
}
