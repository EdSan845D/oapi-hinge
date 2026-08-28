package servergin

import (
	"context"
	"errors"
	"net/http"
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
