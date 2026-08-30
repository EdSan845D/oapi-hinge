package handlers

import (
	"bytes"
	"context"
	_ "embed"
)

//go:embed sample.txt
var sampleData []byte

// DownloadSampleReq 下载示例文件（演示 FileStream 二进制流，数据源为 go:embed 内存数据）
type DownloadSampleReq struct {
	Name string `path:"name" description:"文件名（示例：sample.txt）"`
}

// DownloadSample 下载示例文件
func DownloadSample(ctx context.Context, req DownloadSampleReq, _ any) (*FileStream, error) {
	if req.Name != "sample.txt" {
		return nil, ErrNotFound
	}
	return &FileStream{
		Name:        "sample.txt",
		Size:        int64(len(sampleData)),
		ContentType: "text/plain",
		Reader:      bytes.NewReader(sampleData),
	}, nil
}
