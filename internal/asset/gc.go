package asset

import (
	"context"
	"database/sql"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// GCOptions 控制回收行为。
type GCOptions struct {
	// GracePeriod 是「引用归零」到「真正删除 Blob」之间的冷静期，
	// 期间内的新引用会重置标记（ATK-16）。
	GracePeriod time.Duration
	// BatchSize 单批处理条数，避免长事务。
	BatchSize int
	// DryRun 只统计不删除。
	DryRun bool
}

// DefaultGCOptions 默认：7 天冷静期、每批 200 条。
func DefaultGCOptions() GCOptions {
	return GCOptions{GracePeriod: 7 * 24 * time.Hour, BatchSize: 200}
}

// GCResult 是一次回收的结果。
type GCResult struct {
	Scanned int
	Deleted int
	Skipped int
}

// GC 执行两阶段回收：标记（引用归零的资产）→ 延迟窗口 → 删除。
// 关键不变量：GC 运行期间新建的引用必须让该资产退出回收队列（INV-4）。
func (s *Service) GC(ctx context.Context, opts GCOptions) (GCResult, error) {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 200
	}
	var res GCResult

	// 阶段一：找出「没有活动引用且超过冷静期」的资产。
	// 注意删除条件是 updated_at（软删时间）而非 created_at，
	// 这样「先裸传后引用」的资产不会在冷静期内被清掉。
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.hash
		FROM assets a
		WHERE a.deleted_at IS NOT NULL
		  AND a.deleted_at <= ?
		  AND NOT EXISTS (SELECT 1 FROM asset_refs r WHERE r.asset_id = a.id)
		LIMIT ?`, s.clock.Now().UTC().Add(-opts.GracePeriod), opts.BatchSize)
	if err != nil {
		return res, platform.AsError(err)
	}
	type cand struct{ id, hash string }
	var candidates []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.hash); err != nil {
			rows.Close()
			return res, platform.AsError(err)
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	res.Scanned = len(candidates)

	for _, c := range candidates {
		// 二次确认：并发场景下可能刚被重新引用（乐观检查）
		var refs int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM asset_refs WHERE asset_id = ?`, c.id).Scan(&refs); err != nil {
			return res, platform.AsError(err)
		}
		if refs > 0 {
			res.Skipped++
			continue
		}
		// 物理删除前再查一次：确认没有其他工作区的记录共享同一 hash（内容寻址共享）。
		var sharers int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM assets WHERE hash = ? AND deleted_at IS NULL AND id <> ?`, c.hash, c.id).Scan(&sharers); err != nil {
			return res, platform.AsError(err)
		}
		if sharers > 0 {
			res.Skipped++
			continue
		}
		if opts.DryRun {
			res.Deleted++
			continue
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM assets WHERE id = ?`, c.id); err != nil {
			return res, platform.AsError(err)
		}
		if err := s.blobs.Delete(ctx, c.hash); err != nil {
			return res, err
		}
		res.Deleted++
	}
	return res, nil
}

// AddRef 建立资产引用（画布节点、素材、提示词封面等都走它）。
func (s *Service) AddRef(ctx context.Context, assetID, refType, refID string) error {
	if assetID == "" || refType == "" || refID == "" {
		return platform.ErrInvalid("assetId, refType and refId are required")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO asset_refs (asset_id, ref_type, ref_id, created_at) VALUES (?, ?, ?, ?)`,
		assetID, refType, refID, s.clock.Now().UTC())
	if err != nil && isUnique(err) {
		return nil // 幂等
	}
	return platform.AsError(err)
}

// RemoveRef 解除引用。
func (s *Service) RemoveRef(ctx context.Context, assetID, refType, refID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM asset_refs WHERE asset_id = ? AND ref_type = ? AND ref_id = ?`, assetID, refType, refID)
	return platform.AsError(err)
}

// RefCount 返回资产的引用数（用于删除前提示）。
func (s *Service) RefCount(ctx context.Context, assetID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM asset_refs WHERE asset_id = ?`, assetID).Scan(&n)
	if err != nil && err != sql.ErrNoRows {
		return 0, platform.AsError(err)
	}
	return n, nil
}

// Derive 记录派生关系（裁剪/放大/切图的产物），形成血缘便于「回到原图」。
func (s *Service) Derive(ctx context.Context, childID, parentID, op string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO asset_derivations (child_asset_id, parent_asset_id, op, created_at) VALUES (?, ?, ?, ?)`,
		childID, parentID, op, s.clock.Now().UTC())
	if err != nil && isUnique(err) {
		return nil
	}
	return platform.AsError(err)
}
