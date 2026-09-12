package platform

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 凭据主密钥的管理（INV-5）：密钥只来自配置，永不落库、永不打日志。
//
// 双密钥期（docs/design/13 §4.2「密钥轮换」演练要求）：
//   - 密文带 keyID（= sha256(key) 前 8 字节），解密时按 keyID 判定是否为本密钥；
//   - 轮换 = 用新密钥重写全部密文，旧密钥在切换完成后可删除；
//   - 轮换过程中任一密文解不开就整体失败，不允许「一半新一半旧」的静默状态——
//     那种状态会让部分凭据在切换后永久不可用，比重试一次危险得多。

// GenerateSecretKey 生成合规主密钥（32 字节，base64 输出）。
func GenerateSecretKey() (string, error) {
	key, err := GenerateSecretKeyBytes()
	if err != nil {
		return "", err
	}
	return FormatSecretKey(key), nil
}

// GenerateSecretKeyBytes 生成 32 字节随机密钥。
func GenerateSecretKeyBytes() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成密钥失败: %w", err)
	}
	return key, nil
}

// FormatSecretKey 把密钥格式化为可放入环境变量的字符串。
func FormatSecretKey(key []byte) string { return base64.StdEncoding.EncodeToString(key) }

// DecodeSecretKey 解析密钥（base64 / hex / 原文），只接受 AES 合法长度。
// 与 Config.LoadConfig 共用同一实现，避免「启动能过、CLI 不能过」的分裂。
func DecodeSecretKey(raw string) ([]byte, error) { return decodeKey(raw) }

// RotateResult 是一次密钥轮换的结果。
type RotateResult struct {
	Credentials int    `json:"credentials"`
	Secrets     int    `json:"secrets"`
	DurationMS  int64  `json:"durationMs"`
	Verifier    string `json:"verifier"`
}

// RotateSecretKey 用新密钥重写全部密文（单事务，失败即回滚）。
func RotateSecretKey(ctx context.Context, db *sql.DB, oldKey, newKey []byte) (RotateResult, error) {
	start := time.Now()
	var res RotateResult
	if len(oldKey) == 0 || len(newKey) == 0 {
		return res, errors.New("rotate: 新旧密钥都不能为空")
	}
	if string(oldKey) == string(newKey) {
		return res, errors.New("rotate: 新密钥与旧密钥相同，拒绝执行（会导致无意义重写）")
	}

	oldAEAD, err := newAEAD(oldKey)
	if err != nil {
		return res, err
	}
	newAEAD, err := newAEAD(newKey)
	if err != nil {
		return res, err
	}

	// 先读全量再写：避免在同一事务里边读边写导致的游标与更新互相影响。
	type row struct {
		id  string
		raw []byte
	}
	reencrypt := func(table, idCol, secretCol string) (int, error) {
		rows, err := db.QueryContext(ctx,
			fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s IS NOT NULL AND %s <> ''", idCol, secretCol, table, secretCol, secretCol))
		if err != nil {
			return 0, err
		}
		var pending []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.raw); err != nil {
				_ = rows.Close()
				return 0, err
			}
			pending = append(pending, r)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return 0, err
		}
		_ = rows.Close()

		for _, r := range pending {
			plain, err := openWith(oldAEAD, r.raw)
			if err != nil {
				// 解不开就整体失败：静默跳过会导致「部分凭据轮换后无法使用」，
				// 这种半状态比重试一次危险得多。
				return 0, fmt.Errorf("rotate %s/%s: %w", table, r.id, err)
			}
			sealed, err := sealWith(newAEAD, keyIDBytes(newKey), plain)
			if err != nil {
				return 0, err
			}
			if _, err := db.ExecContext(ctx,
				fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ?", table, secretCol, idCol), sealed, r.id); err != nil {
				return 0, err
			}
		}
		return len(pending), nil
	}

	// 轮换的落点是「存密文的列」。表不存在时跳过（精简部署可能没有插件表）。
	for _, t := range []struct{ table, idCol, secretCol string }{
		{"provider_credentials", "id", "secret_ref"},
		{"plugins", "plugin_key", "config"},
	} {
		n, err := reencrypt(t.table, t.idCol, t.secretCol)
		if err != nil {
			if isMissingTable(err) {
				continue // 表未建（精简部署）时跳过，而不是整体失败
			}
			return res, err
		}
		switch t.table {
		case "provider_credentials":
			res.Credentials = n
		case "plugins":
			res.Secrets = n
		}
	}
	res.Verifier = KeyID(newKey)
	res.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

// KeyID 返回密钥的短标识（用于密文头与轮换核对）。
func KeyID(key []byte) string { return hex.EncodeToString(keyIDBytes(key)) }

// SealSecret 用主密钥加密明文（供 provider / plugin 使用）。
func SealSecret(key []byte, plaintext string) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return sealWith(aead, keyIDBytes(key), []byte(plaintext))
}

// OpenSecret 用主密钥解密密文。若密文的 keyID 与当前密钥不匹配，
// 返回 SentinelKeyMismatch（便于轮换流程给出「需要换回旧密钥」的明确提示）。
func OpenSecret(key []byte, raw []byte) (string, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	s, err := splitSealed(aead, raw)
	if err != nil {
		return "", err
	}
	if !equalBytes(s.keyID, keyIDBytes(key)) {
		return "", SentinelKeyMismatch
	}
	plain, err := openWith(aead, raw)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// SentinelKeyMismatch 表示密文不是用当前主密钥加密的。
var SentinelKeyMismatch = errors.New("密文由其他主密钥加密（keyId 不匹配）")

func keyIDBytes(key []byte) []byte {
	sum := sha256.Sum256(key)
	return sum[:sealedKeyIDLen]
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("主密钥无效: %w", err)
	}
	return cipher.NewGCM(block)
}

// sealedHeaderLen 是密文头部长度：keyID(8) + nonce(12)。
const sealedKeyIDLen = 8

// sealed 结构：keyID(8) || nonce(12) || ciphertext。
// keyID 作为 AEAD 的 additional data：密文被挪到别的 keyID 下时认证直接失败，
// 从而把「密钥不匹配」变成可判定错误，而不是靠试解密碰运气。
type sealed struct {
	keyID []byte
	nonce []byte
	body  []byte
}

func splitSealed(aead cipher.AEAD, raw []byte) (sealed, error) {
	if len(raw) < sealedKeyIDLen+aead.NonceSize() {
		return sealed{}, errors.New("密文过短或格式不正确")
	}
	return sealed{
		keyID: raw[:sealedKeyIDLen],
		nonce: raw[sealedKeyIDLen : sealedKeyIDLen+aead.NonceSize()],
		body:  raw[sealedKeyIDLen+aead.NonceSize():],
	}, nil
}

// sealWith 用 key 加密，并把 keyID 写进头部。
func sealWith(aead cipher.AEAD, keyID []byte, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, sealedKeyIDLen+len(nonce)+len(plaintext)+aead.Overhead())
	out = append(out, keyID...)
	out = append(out, nonce...)
	return aead.Seal(out, nonce, plaintext, keyID), nil
}

// openWith 解密并校验 keyID 与 additional data 一致。
func openWith(aead cipher.AEAD, raw []byte) ([]byte, error) {
	s, err := splitSealed(aead, raw)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, s.nonce, s.body, s.keyID)
	if err != nil {
		return nil, errors.New("解密失败（主密钥不匹配或密文损坏）")
	}
	return plain, nil
}

// ExportManifest 导出结构清单：每张表的行数 + 关键外键计数 + schema 版本。
// 用途是「备份恢复演练」时核对恢复结果，而不是业务数据导出。
func ExportManifest(ctx context.Context, db *sql.DB) (map[string]any, error) {
	tables := []string{
		"users", "workspaces", "workspace_members", "projects", "canvases", "canvas_ops", "canvas_docs",
		"assets", "asset_refs", "asset_derivations", "runs", "run_steps", "run_attempts",
		"providers", "provider_credentials", "prompt_sources", "prompts", "prompt_sync_logs",
		"plugins", "agent_sessions", "agent_turns", "agent_items", "audit_logs",
	}
	counts := map[string]any{}
	total := 0
	for _, t := range tables {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+t).Scan(&n); err != nil {
			if isMissingTable(err) {
				counts[t] = "n/a"
				continue
			}
			return nil, fmt.Errorf("统计 %s: %w", t, err)
		}
		counts[t] = n
		total += n
	}
	migrations, err := (&DB{DB: db, Dialect: "sqlite"}).AppliedMigrations(ctx)
	if err != nil {
		migrations = nil
	}
	return map[string]any{
		"app":        "ic",
		"version":    Version,
		"commit":     Commit,
		"exportedAt": time.Now().UTC().Format(time.RFC3339),
		"tables":     counts,
		"totalRows":  total,
		"migrations": migrations,
		"note":       "本清单不含资产二进制；请同时备份 Blob 目录以完成可恢复备份。",
	}, nil
}

func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist")
}

// MarshalSealed 便于测试与调试时观察密文结构（不含密钥）。
func MarshalSealed(sealed []byte) string {
	if len(sealed) < 8 {
		return "{}"
	}
	raw, _ := json.Marshal(map[string]any{
		"keyId": hex.EncodeToString(sealed[:8]),
		"bytes": len(sealed),
	})
	return string(raw)
}
