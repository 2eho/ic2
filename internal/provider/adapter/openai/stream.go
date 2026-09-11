package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// openStream 打开 /v1/responses 的 SSE 流。
// 只解析 data: 帧，忽略心跳与注释；[DONE] 表示结束。
func (a *Adapter) openStream(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Stream, error) {
	body := responsesReq{
		Model:  req.Model,
		Input:  ComposePrompt(req),
		Stream: true,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, platform.AsError(err)
	}
	headers := provider.AuthHeaders(cred)
	headers["Accept"] = "text/event-stream"
	rc, _, err := a.transport.DoRawWithBody(ctx, http.MethodPost,
		provider.TrimBaseURL(cred.BaseURL)+"/v1/responses", headers, payload)
	if err != nil {
		return nil, err
	}
	return &sseStream{rc: rc, sc: bufio.NewScanner(rc)}, nil
}

type sseStream struct {
	rc     io.ReadCloser
	sc     *bufio.Scanner
	closed bool
}

type streamDelta struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	Text  string `json:"text"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Recv 见 provider.Stream。
func (s *sseStream) Recv() (string, bool, error) {
	if s.closed {
		return "", true, nil
	}
	// 加大单行缓冲：模型可能一次吐出较长增量
	s.sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for s.sc.Scan() {
		line := strings.TrimSpace(s.sc.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return "", true, nil
		}
		var d streamDelta
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			continue
		}
		if d.Error != nil {
			return "", true, &provider.ProviderError{
				Class: provider.ClassPermanent, Code: platform.CodeUpstreamInvalid, Message: d.Error.Message,
			}
		}
		switch d.Type {
		case "response.output_text.delta":
			if d.Delta != "" {
				return d.Delta, false, nil
			}
		case "response.completed", "response.done":
			return "", true, nil
		default:
			if d.Text != "" && d.Delta == "" {
				return d.Text, false, nil
			}
		}
	}
	if err := s.sc.Err(); err != nil {
		return "", true, provider.ClassifyTransport(err)
	}
	return "", true, nil
}

// Close 见 provider.Stream。
func (s *sseStream) Close() error {
	s.closed = true
	return s.rc.Close()
}
