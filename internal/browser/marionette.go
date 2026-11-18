package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// marionetteClient 是 Firefox Marionette 协议的最小实现。
// 协议使用“长度:JSON”分帧，命令格式为 [0, id, "Command", params]，
// 响应格式为 [1, id, error, result]。
type marionetteClient struct {
	conn net.Conn
	br   *bufio.Reader
	mu   sync.Mutex
	next int
}

func dialMarionette(ctx context.Context, addr string) (*marionetteClient, error) {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &marionetteClient{conn: conn, br: bufio.NewReader(conn)}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	raw, err := readMarionetteMessage(c.br)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取 Marionette 握手失败: %w", err)
	}
	var hello struct {
		ApplicationType string `json:"applicationType"`
	}
	if err := json.Unmarshal(raw, &hello); err != nil || hello.ApplicationType != "gecko" {
		conn.Close()
		return nil, fmt.Errorf("不是有效的 Marionette 握手: %s", truncateText(string(raw), 120))
	}
	return c, nil
}

func (c *marionetteClient) close() {
	if c == nil || c.conn == nil {
		return
	}
	_ = c.conn.Close()
}

// call 发送一条 Marionette 命令并等待对应 id 的响应。
func (c *marionetteClient) call(ctx context.Context, name string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	c.next++
	id := c.next
	msg := []interface{}{0, id, name, params}
	if err := writeMarionetteMessage(c.conn, msg); err != nil {
		return nil, fmt.Errorf("发送 %s 失败: %w", name, err)
	}
	for {
		raw, err := readMarionetteMessage(c.br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("%s 失败: Firefox 已关闭连接", name)
			}
			return nil, fmt.Errorf("%s 失败: %w", name, err)
		}
		var resp []json.RawMessage
		if err := json.Unmarshal(raw, &resp); err != nil || len(resp) != 4 {
			return nil, fmt.Errorf("无效的 Marionette 响应: %s", truncateText(string(raw), 200))
		}
		var kind, respID int
		_ = json.Unmarshal(resp[0], &kind)
		_ = json.Unmarshal(resp[1], &respID)
		if kind != 1 || respID != id {
			// 忽略事件或过期响应。
			continue
		}
		if string(resp[2]) != "null" {
			var werr struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal(resp[2], &werr)
			text := strings.TrimSpace(werr.Message)
			if text == "" {
				text = truncateText(string(resp[2]), 300)
			}
			if werr.Error != "" {
				return nil, fmt.Errorf("%s 失败（%s）: %s", name, werr.Error, text)
			}
			return nil, fmt.Errorf("%s 失败: %s", name, text)
		}
		return resp[3], nil
	}
}

func writeMarionetteMessage(w io.Writer, msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%d:", len(data)); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func readMarionetteMessage(r *bufio.Reader) ([]byte, error) {
	var length strings.Builder
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == ':' {
			break
		}
		if b < '0' || b > '9' {
			return nil, fmt.Errorf("无效的消息长度前缀 %q", b)
		}
		length.WriteByte(b)
		if length.Len() > 16 {
			return nil, errors.New("消息长度过长")
		}
	}
	n, err := strconv.Atoi(length.String())
	if err != nil || n < 0 {
		return nil, fmt.Errorf("无效的消息长度 %q", length.String())
	}
	if n > 64<<20 {
		return nil, errors.New("消息超过 64MB")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}

// executeScript 执行页面脚本并返回脚本的返回值（JSON 编码）。
func (c *marionetteClient) executeScript(ctx context.Context, script string) (json.RawMessage, error) {
	raw, err := c.call(ctx, "WebDriver:ExecuteScript", map[string]interface{}{
		"script":  script,
		"args":    []interface{}{},
		"sandbox": "default",
	}, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("解析脚本返回值失败: %w", err)
	}
	return payload.Value, nil
}

func (c *marionetteClient) executeString(ctx context.Context, script string) (string, error) {
	raw, err := c.executeScript(ctx, script)
	if err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("脚本返回值不是字符串: %s", truncateText(string(raw), 200))
	}
	return value, nil
}

type webdriverCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

func (c *marionetteClient) cookies(ctx context.Context) ([]webdriverCookie, error) {
	raw, err := c.call(ctx, "WebDriver:GetCookies", map[string]interface{}{}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var out []webdriverCookie
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析 Cookie 失败: %w", err)
	}
	return out, nil
}
