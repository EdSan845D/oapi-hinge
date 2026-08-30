package serverecho

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/EdSan845D/oapi-hinge/contract"
)

// ============ 与 servergin 对齐：字段级错误明细 + cookie 标签 ============
// echoCallHdr 复用 stream_cid_agg_test.go 中的同签名 helper。

type echoBadQueryReq struct {
	Page int    `query:"page"`
	Size int    `query:"size"`
	Tag  string `query:"tag" binding:"required"`
}

func TestEchoBindFieldDetails(t *testing.T) {
	h := func(ctx context.Context, q echoBadQueryReq, _ any) (map[string]any, error) {
		return map[string]any{"page": q.Page}, nil
	}
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/t",
		Routes: []contract.Route{contract.New(contract.RouteMeta[echoBadQueryReq, any, map[string]any]{Method: "GET", Path: "", Handler: h})},
	}})

	w := echoCallHdr(t, e, http.MethodGet, "/t?page=abc&size=xyz&tag=x", "", nil)
	var body struct {
		Code       int `json:"code"`
		BindErrors []struct {
			Field string `json:"field"`
			In    string `json:"in"`
		} `json:"bind_errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.BindErrors) != 2 {
		t.Fatalf("bind_errors = %+v", body.BindErrors)
	}
	if body.BindErrors[0].Field != "page" || body.BindErrors[1].Field != "size" {
		t.Fatalf("fields = %+v", body.BindErrors)
	}
}

type echoCookieReq struct {
	SID string `cookie:"sid"`
	UID string `cookie:"uid" default:"guest"`
}

func TestEchoCookieBinding(t *testing.T) {
	h := func(ctx context.Context, q echoCookieReq, _ any) (map[string]string, error) {
		return map[string]string{"sid": q.SID, "uid": q.UID}, nil
	}
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/c",
		Routes: []contract.Route{contract.New(contract.RouteMeta[echoCookieReq, any, map[string]string]{Method: "GET", Path: "", Handler: h})},
	}})

	w := echoCallHdr(t, e, http.MethodGet, "/c", "", map[string]string{"Cookie": "sid=s-123"})
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
	if body.Data["uid"] != "guest" {
		t.Fatalf("uid = %q", body.Data["uid"])
	}
}

type echoRequiredReq struct {
	A string `query:"a" binding:"required"`
	B string `query:"b" binding:"required"`
}

func TestEchoRequiredFieldsDetail(t *testing.T) {
	h := func(ctx context.Context, q echoRequiredReq, _ any) (map[string]any, error) { return nil, nil }
	e := newEchoServerFor(t, nil, []*contract.Group{{
		Prefix: "/r",
		Routes: []contract.Route{contract.New(contract.RouteMeta[echoRequiredReq, any, map[string]any]{Method: "GET", Path: "", Handler: h})},
	}})
	w := echoCallHdr(t, e, http.MethodGet, "/r", "", nil)
	var body struct {
		BindErrors []struct {
			Field string `json:"field"`
		} `json:"bind_errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.BindErrors) != 2 || body.BindErrors[0].Field != "a" || body.BindErrors[1].Field != "b" {
		t.Fatalf("bind_errors = %+v", body.BindErrors)
	}
}
