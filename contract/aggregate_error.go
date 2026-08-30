package contract

// ItemError 批量操作中的单项失败明细。
// Key 由业务层决定语义（批次索引、业务 ID 等），用于客户端定位失败项。
type ItemError struct {
	Key  string `json:"key"`
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// AggregateError 「整体受理、部分失败」的批量错误。
//
// 整体语义（HTTP 状态码 / 业务码 / 对外信息）走 StatusError 的常规错误决策；
// 逐项失败明细由实现 response.AggregateEnvelope 的响应壳输出到壳的
// aggregated_error 字段（默认壳 {code, data, msg} 原生支持；未实现该可选接口的
// 自定义壳保持原行为，仅输出整体错误）。
//
// 用法：
//
//	return result, &AggregateError{
//	    StatusError: StatusError{Msg: "部分文件复制失败"},
//	    Total:       3,
//	    Failed:      []ItemError{{Key: "b.txt", Code: 7, Msg: "目标已存在"}},
//	}
type AggregateError struct {
	StatusError
	Total  int         // 批量总数
	Failed []ItemError // 失败明细
}
