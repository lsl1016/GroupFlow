package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 是基于 Elasticsearch REST API 的轻量客户端（仅依赖标准库 net/http）。
type Client struct {
	addrs []string
	index string
	http  *http.Client
}

// NewClient 创建 ES 客户端。addrs 为节点地址列表（http://host:9200），index 为目标索引/别名。
func NewClient(addrs []string, index string) *Client {
	return &Client{
		addrs: addrs,
		index: index,
		http:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Index 返回目标索引名。
func (c *Client) IndexName() string { return c.index }

func (c *Client) base() string {
	if len(c.addrs) == 0 {
		return ""
	}
	return strings.TrimRight(c.addrs[0], "/")
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("elasticsearch %s %s: status %d: %s", method, path, resp.StatusCode, string(raw))
	}
	return raw, nil
}

// Search 对目标索引执行查询，返回原始响应体。
func (c *Client) Search(ctx context.Context, query map[string]any) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/"+c.index+"/_search", query)
}

// Index 以 docID 为 _id 幂等写入/覆盖一条文档（PUT _doc/{id}）。
func (c *Client) Index(ctx context.Context, docID string, doc map[string]any) error {
	_, err := c.do(ctx, http.MethodPut, "/"+c.index+"/_doc/"+docID, doc)
	return err
}

// UpdateStatus 仅更新某条文档的 status 字段（用于撤回/删除同步）。文档不存在时忽略。
func (c *Client) UpdateStatus(ctx context.Context, docID, status string) error {
	body := map[string]any{"doc": map[string]any{"status": status}}
	_, err := c.do(ctx, http.MethodPost, "/"+c.index+"/_update/"+docID, body)
	return err
}

// EnsureIndex 在索引不存在时按给定 mapping 创建；已存在则跳过。best-effort，失败由调用方决定是否致命。
func (c *Client) EnsureIndex(ctx context.Context, mapping map[string]any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.base()+"/"+c.index, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	_, err = c.do(ctx, http.MethodPut, "/"+c.index, mapping)
	return err
}

// Bulk 以 NDJSON 批量写入（用于存量回填）。caller 负责拼装动作行。
func (c *Client) Bulk(ctx context.Context, ndjson []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/_bulk", bytes.NewReader(ndjson))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("elasticsearch bulk: status %d: %s", resp.StatusCode, string(raw))
	}
	return raw, nil
}
