package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
)

// ComposePrompt 见 provider.ComposePrompt。
func ComposePrompt(req provider.Request) string { return provider.ComposePrompt(req) }

// rawMultipart 把已构造好的 multipart 字节流交给传输层，避免二次 JSON 编码。
func rawMultipart(body, contentType string) provider.RawBodyType {
	return provider.RawBody([]byte(body), contentType)
}

// writeDataURIPart 把 data URI 的 base64 内容写为 multipart 文件字段。
func writeDataURIPart(mw *multipart.Writer, field, filename, header, b64 string) error {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("decode data uri: %w", err)
	}
	mime := strings.TrimPrefix(header, "data:")
	mime = strings.TrimSuffix(mime, ";base64")
	part, err := mw.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="` + field + `"; filename="` + filename + `"`},
		"Content-Type":        {mime},
	})
	if err != nil {
		return err
	}
	_, err = part.Write(data)
	return err
}

func readAllLimit(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit))
}

// postJSONForBinary 发起 JSON 请求并返回二进制响应（音频）。
func postJSONForBinary(tr *provider.Transport, ctx context.Context, url string,
	headers map[string]string, body any) (io.ReadCloser, string, error) {

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, "", platform.AsError(err)
	}
	return tr.DoRawWithBody(ctx, http.MethodPost, url, headers, payload)
}
