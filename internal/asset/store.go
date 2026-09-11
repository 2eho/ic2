// Package asset 实现内容寻址的资产存储：去重、缩略图、引用计数与 GC。
package asset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/context-flow/ic/internal/platform"
)

// BlobStore 是二进制存储抽象。解耦清单要求 ≥2 个实现：FS / 内存。
type BlobStore interface {
	Put(ctx context.Context, hash string, r io.Reader, size int64, mime string) error
	Open(ctx context.Context, hash string) (io.ReadSeekCloser, int64, error)
	Exists(ctx context.Context, hash string) (bool, error)
	Delete(ctx context.Context, hash string) error
}

// FSBlobStore 是文件系统实现。路径完全由 hash 推导，不使用任何用户提供的文件名，
// 从根上杜绝路径穿越（ATK-05 / INV-4）。
type FSBlobStore struct {
	Root string
	mu   sync.Mutex
}

// NewFSBlobStore 创建文件系统 Blob 存储。
func NewFSBlobStore(root string) (*FSBlobStore, error) {
	if root == "" {
		return nil, errors.New("blob root must not be empty")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	return &FSBlobStore{Root: root}, nil
}

// pathFor 返回 hash 对应的文件路径：root/ab/cd/<hash>。
func (s *FSBlobStore) pathFor(hash string) (string, error) {
	if !validHash(hash) {
		return "", platform.NewError(400, platform.CodeInvalidRequest, "invalid content hash")
	}
	return filepath.Join(s.Root, hash[0:2], hash[2:4], hash), nil
}

// Put 写入内容（已存在则跳过，实现秒传）。
func (s *FSBlobStore) Put(_ context.Context, hash string, r io.Reader, _ int64, _ string) error {
	p, err := s.pathFor(hash)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(p); err == nil {
		return nil // 内容寻址：命中即复用
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return platform.AsError(err)
	}
	// 先写临时文件再原子改名，避免半写文件被读到。
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return platform.AsError(err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return platform.AsError(err)
	}
	if err := tmp.Close(); err != nil {
		return platform.AsError(err)
	}
	return platform.AsError(os.Rename(tmpName, p))
}

// Open 打开内容用于读取（支持 Range 所需的 Seek）。
func (s *FSBlobStore) Open(_ context.Context, hash string) (io.ReadSeekCloser, int64, error) {
	p, err := s.pathFor(hash)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, platform.ErrNotFound("blob")
		}
		return nil, 0, platform.AsError(err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, platform.AsError(err)
	}
	return f, st.Size(), nil
}

// Exists 判断内容是否存在。
func (s *FSBlobStore) Exists(_ context.Context, hash string) (bool, error) {
	p, err := s.pathFor(hash)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(p); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, platform.AsError(err)
	}
}

// Delete 删除内容（幂等）。
func (s *FSBlobStore) Delete(_ context.Context, hash string) error {
	p, err := s.pathFor(hash)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return platform.AsError(err)
	}
	// 清理空目录（忽略错误）
	_ = os.Remove(filepath.Dir(p))
	return nil
}

func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// HashReader 边读边算 sha256，返回 hash 与被读取的字节数。
// 调用方随后用返回的 bytes 再次构造 reader（内容寻址需要先知道 hash 才能落盘）。
func HashReader(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, platform.AsError(err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// MemoryBlobStore 是测试替身。
type MemoryBlobStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryBlobStore 创建内存 Blob 存储。
func NewMemoryBlobStore() *MemoryBlobStore {
	return &MemoryBlobStore{data: map[string][]byte{}}
}

// Put 见 BlobStore。
func (s *MemoryBlobStore) Put(_ context.Context, hash string, r io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return platform.AsError(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[hash] = b
	return nil
}

type memReader struct {
	data   []byte
	offset int
}

func (r *memReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func (r *memReader) Seek(offset int64, whence int) (int64, error) {
	var next int
	switch whence {
	case io.SeekStart:
		next = int(offset)
	case io.SeekCurrent:
		next = r.offset + int(offset)
	case io.SeekEnd:
		next = len(r.data) + int(offset)
	default:
		return 0, errors.New("invalid whence")
	}
	if next < 0 || next > len(r.data) {
		return int64(r.offset), errors.New("seek out of range")
	}
	r.offset = next
	return int64(next), nil
}

func (r *memReader) Close() error { return nil }

// Open 见 BlobStore。
func (s *MemoryBlobStore) Open(_ context.Context, hash string) (io.ReadSeekCloser, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.data[hash]
	if !ok {
		return nil, 0, platform.ErrNotFound("blob")
	}
	return &memReader{data: b}, int64(len(b)), nil
}

// Exists 见 BlobStore。
func (s *MemoryBlobStore) Exists(_ context.Context, hash string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data[hash]
	return ok, nil
}

// Delete 见 BlobStore。
func (s *MemoryBlobStore) Delete(_ context.Context, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, hash)
	return nil
}

// Size 返回条目数（测试断言用）。
func (s *MemoryBlobStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}
