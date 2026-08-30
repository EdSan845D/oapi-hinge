package contract

import (
	"context"
	"crypto/rand"
	"fmt"
)

// HeaderCorrelationID 请求关联 ID 的 HTTP 头：入站沿用（客户端/网关传入），
// 出站回写同一值，客户端可与日志互查。
const HeaderCorrelationID = "X-Correlation-Id"

// CorrelationIDCtx 关联 ID 的 context key。
// 开启适配器的关联 ID 注入（servergin.SetCorrelation / serverecho.SetCorrelation）后，
// 每个请求的 ctx 保证携带该值，业务层与 decorator 均可读取。
type CorrelationIDCtx struct{}

// NewCorrelationID 生成 UUIDv4 形态的关联 ID（随机源 crypto/rand，无第三方依赖）。
func NewCorrelationID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// WithCorrelationID 将关联 ID 注入 context（适配器在请求管线最前端调用）。
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, CorrelationIDCtx{}, id)
}

// CorrelationIDFrom 从 context 读取关联 ID；未注入时返回空串。
func CorrelationIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(CorrelationIDCtx{}).(string)
	return id
}
