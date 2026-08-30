package servergin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"
)

// ============ 新特性测试：字段级绑定/校验错误明细 + cookie 标签 ============

type badQueryReq struct {
	Page int    `query:"page"`
	Size int    `query:"size"`
	Tag  string `query:"tag" binding:"required"`
}

func bindDetailHandler(ctx context.Context, q badQueryReq, _ any) (map[string]any, error) {
	return map[string]any{"page": q.Page}, nil
}

// 两个 int 字段都传非数字：两条字段级明细（不快速失败），In=query
func TestBindFieldDetails(t *testing.T) {
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/t",
		Routes: []contract.Route{contract.New(contract.RouteMeta[badQueryReq, any, map[string]any]{Method: "GET", Path: "", Handler: bindDetailHandler})},
	}})

	w := callHdr(t, e, http.MethodGet, "/t?page=abc&size=xyz&tag=x", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var body struct {
		Code       int `json:"code"`
		BindErrors []struct {
			Field string `json:"field"`
			In    string `json:"in"`
			Msg   string `json:"msg"`
		} `json:"bind_errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.BindErrors) != 2 {
		t.Fatalf("bind_errors = %+v", body.BindErrors)
	}
	if body.BindErrors[0].Field != "page" || body.BindErrors[0].In != "query" {
		t.Fatalf("first = %+v", body.BindErrors[0])
	}
	if body.BindErrors[1].Field != "size" || body.BindErrors[1].In != "query" {
		t.Fatalf("second = %+v", body.BindErrors[1])
	}
}

type requiredReq struct {
	A string `query:"a" binding:"required"`
	B string `query:"b" binding:"required"`
}

// 两个必填同时缺失：validator.Run 聚合输出两条明细
func TestRequiredFieldsDetail(t *testing.T) {
	h := func(ctx context.Context, q requiredReq, _ any) (map[string]any, error) { return nil, nil }
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/r",
		Routes: []contract.Route{contract.New(contract.RouteMeta[requiredReq, any, map[string]any]{Method: "GET", Path: "", Handler: h})},
	}})

	w := callHdr(t, e, http.MethodGet, "/r", "", nil)
	var body struct {
		BindErrors []struct {
			Field string `json:"field"`
			In    string `json:"in"`
			Msg   string `json:"msg"`
		} `json:"bind_errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.BindErrors) != 2 || body.BindErrors[0].Field != "a" || body.BindErrors[1].Field != "b" {
		t.Fatalf("bind_errors = %+v", body.BindErrors)
	}
}

type jsonTypedReq struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// JSON body 字段类型错误：定位到具体字段（UnmarshalTypeError.Field）
func TestJSONTypeErrorField(t *testing.T) {
	h := func(ctx context.Context, _ contract.NoReq, b jsonTypedReq) (map[string]any, error) {
		return nil, nil
	}
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/j",
		Routes: []contract.Route{contract.New(contract.RouteMeta[contract.NoReq, jsonTypedReq, map[string]any]{Method: "POST", Path: "", Handler: h})},
	}})

	w := callRaw(t, e, http.MethodPost, "/j", strings.NewReader(`{"name":"x","age":"not-a-number"}`), "application/json")
	var body struct {
		BindErrors []struct {
			Field string `json:"field"`
			In    string `json:"in"`
		} `json:"bind_errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.BindErrors) != 1 || body.BindErrors[0].Field != "age" || body.BindErrors[0].In != "body" {
		t.Fatalf("bind_errors = %+v", body.BindErrors)
	}
}

type cookieReq struct {
	SID string `cookie:"sid"`
	UID string `cookie:"uid" default:"guest"`
}

func cookieHandler(ctx context.Context, q cookieReq, _ any) (map[string]string, error) {
	return map[string]string{"sid": q.SID, "uid": q.UID}, nil
}

// cookie 标签：有值绑定；缺失走 default；不缺 required 时正常通过
func TestCookieBinding(t *testing.T) {
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/c",
		Routes: []contract.Route{contract.New(contract.RouteMeta[cookieReq, any, map[string]string]{Method: "GET", Path: "", Handler: cookieHandler})},
	}})

	w := callHdr(t, e, http.MethodGet, "/c", "", map[string]string{"Cookie": "sid=s-123"})
	var body struct {
		Code int               `json:"code"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Data["sid"] != "s-123" {
		t.Fatalf("sid = %q", body.Data["sid"])
	}
	// uid cookie 缺失 → default 填充 guest
	if body.Data["uid"] != "guest" {
		t.Fatalf("uid = %q", body.Data["uid"])
	}
}

// 普通业务错误不携带 bind_errors 键
func TestBusinessErrorNoBindErrorsKey(t *testing.T) {
	h := func(ctx context.Context, q cookieReq, _ any) (map[string]string, error) {
		return nil, contract.NotFound("用户不存在")
	}
	e := newTestEngineFor(t, nil, []*contract.Group{{
		Prefix: "/c",
		Routes: []contract.Route{contract.New(contract.RouteMeta[cookieReq, any, map[string]string]{Method: "GET", Path: "", Handler: h})},
	}})
	w := callHdr(t, e, http.MethodGet, "/c", "", map[string]string{"Cookie": "sid=x"})
	if strings.Contains(w.Body.String(), "bind_errors") {
		t.Fatalf("business error should not carry bind_errors: %s", w.Body.String())
	}
}
