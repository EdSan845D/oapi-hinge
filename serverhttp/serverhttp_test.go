package serverhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EdSan845D/oapi-hinge/hinge"
)

// 内核行为测试（经 stdlib transport）：壳 / 错误链 / 绑定明细 / 路径参数 / 关联 ID。
// 手写绑定器模拟生成产物形态（hinge.Parse 直调，零反射）。

type tGetReq struct {
	ID  string `path:"id"`
	Lang string `header:"Accept-Language"`
}

type tCreateReq struct {
	Name string `json:"name" binding:"required"`
}

type tUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func bindGet(ctx context.Context, r hinge.RequestReader) (any, error) {
	var v tGetReq
	if raw, ok := r.PathParam("id"); ok {
		s, err := hinge.Parse[string](raw, "id")
		if err != nil {
			return v, err
		}
		v.ID = s
	}
	if raw, ok := r.Header("Accept-Language"); ok {
		v.Lang = raw
	}
	return v, nil
}

func bindCreate(ctx context.Context, r hinge.RequestReader) (any, error) {
	var v tCreateReq
	data, err := r.Body()
	if err != nil {
		return v, err
	}
	if len(data) > 0 {
		if err := hinge.DecodeJSON(data, &v); err != nil {
			if be := hinge.AsBindError(err); be != nil {
				return v, be
			}
			return v, err
		}
	}
	be := &hinge.BindError{}
	if v.Name == "" {
		be.AddField("name", "body", "is required")
		return v, be
	}
	return v, nil
}

func setup(t *testing.T, withCorrelation bool) *http.ServeMux {
	t.Helper()
	k := NewKernel()
	k.SetCorrelation(withCorrelation)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/users/{id}", Handle(k, hinge.Endpoint{
		Owner: "T", Handler: "Get", Method: "GET", Path: "/api/users/{id}", Summary: "详情",
		QType: hinge.Type[tGetReq](), RType: hinge.Type[tUser](),
	}, bindGet, nil, func(ctx context.Context, q, b any) (any, error) {
		v := q.(tGetReq)
		if v.ID == "missing" {
			return nil, hinge.NotFound("用户不存在")
		}
		return tUser{ID: v.ID, Name: "alice"}, nil
	}))
	mux.HandleFunc("POST /api/users", Handle(k, hinge.Endpoint{
		Owner: "T", Handler: "Create", Method: "POST", Path: "/api/users", Summary: "创建",
		BType: hinge.Type[tCreateReq](), RType: hinge.Type[tUser](),
	}, nil, bindCreate, func(ctx context.Context, q, b any) (any, error) {
		return tUser{ID: "u9", Name: b.(tCreateReq).Name}, nil
	}))
	return mux
}

func TestKernelEnvelopeAndPathParams(t *testing.T) {
	srv := httptest.NewServer(setup(t, false))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/users/u1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Code int    `json:"code"`
		Data tUser  `json:"data"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 0 || body.Data.ID != "u1" || body.Data.Name != "alice" {
		t.Fatalf("envelope mismatch: %+v", body)
	}
}

func TestKernelNotFoundErrorMapping(t *testing.T) {
	srv := httptest.NewServer(setup(t, false))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/users/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var body struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Code != 404 || !strings.Contains(body.Msg, "用户不存在") {
		t.Fatalf("error envelope mismatch: %+v", body)
	}
}

func TestKernelBindFieldErrors(t *testing.T) {
	srv := httptest.NewServer(setup(t, false))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/users", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// 默认 bindStatus=200（存量语义）：HTTP 200 + code=7 + bind_errors 明细
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Code       int    `json:"code"`
		Msg        string `json:"msg"`
		BindErrors []struct {
			Field string `json:"field"`
			In    string `json:"in"`
			Msg   string `json:"msg"`
		} `json:"bind_errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 7 {
		t.Fatalf("code = %d, want 7", body.Code)
	}
	if len(body.BindErrors) == 0 || body.BindErrors[0].Field != "name" || body.BindErrors[0].In != "body" {
		t.Fatalf("bind_errors mismatch: %+v", body)
	}
}

func TestKernelCreateSuccess(t *testing.T) {
	srv := httptest.NewServer(setup(t, false))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/users", "application/json", strings.NewReader(`{"name":"bob"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Code int   `json:"code"`
		Data tUser `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Code != 0 || body.Data.Name != "bob" {
		t.Fatalf("create envelope mismatch: %+v", body)
	}
}

func TestKernelCorrelation(t *testing.T) {
	srv := httptest.NewServer(setup(t, true))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/users/u1", nil)
	req.Header.Set("X-Correlation-Id", "cid-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Correlation-Id"); got != "cid-123" {
		t.Fatalf("correlation id = %q, want cid-123 (入站沿用)", got)
	}
}
