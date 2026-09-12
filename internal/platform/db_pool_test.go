package platform

import (
	"context"
	"testing"
)

// 连接池大小必须 > 1。
//
// 事实经过：SQLite 版本把 MaxOpenConns 限成 1（「单写者」），这在
// **一个请求内需要多次查询**的场景下会自锁——外层查询的 Rows 还没读完，
// 内层查询就在等同一条连接。实测表现极具误导性：
//
//	报错是 "context deadline exceeded"
//	真实原因是「连接池饿死」
//
// 更糟的是它只在「一次请求里跨领域查询」时出现（认证读工作区 + 渠道读凭据），
// 所以功能测试很容易漏掉，而线上表现为「间歇性 500」。
//
// 这条用例把连接数要求钉死：单写者语义由 SQLite 自身的文件锁保证，
// 不依赖连接数。
func TestSQLitePoolAllowsNestedQueries(t *testing.T) {
	cfg := Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = t.TempDir() + "/pool.db"
	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	stats := db.DB.Stats()
	if stats.MaxOpenConnections < 2 {
		t.Fatalf(
			"MaxOpenConns=%d：一次请求内的嵌套查询会饿死连接池，表现是"+
				"「context deadline exceeded」而不是「没有可用凭据」这类真实原因",
			stats.MaxOpenConnections,
		)
	}
}

// 嵌套查询必须真的能跑通（不只看配置数字）。
//
// 这条用例模拟真实形态：持有一个未读完的 Rows 句柄，同时发起另一个查询。
// 配置是对的但驱动行为不同（例如某些 WAL 设置）时，这条会红。
func TestNestedQueriesDoNotDeadlock(t *testing.T) {
	cfg := Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = t.TempDir() + "/nested.db"
	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.DB.ExecContext(ctx, `CREATE TABLE a (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if _, err := db.DB.ExecContext(ctx, `CREATE TABLE b (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO a (id, v) VALUES (1, 'x'), (2, 'y')`); err != nil {
		t.Fatalf("插入失败: %v", err)
	}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO b (id, v) VALUES (1, 'z')`); err != nil {
		t.Fatalf("插入失败: %v", err)
	}

	// 外层查询：故意在遍历过程中不关闭，模拟「handler 持有 Rows」的形态。
	rows, err := db.DB.QueryContext(ctx, `SELECT id, v FROM a ORDER BY id`)
	if err != nil {
		t.Fatalf("外层查询失败: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		// 内层查询：如果连接池只有 1 条连接，这里会阻塞到超时。
		var v string
		if err := db.DB.QueryRowContext(ctx, `SELECT v FROM b WHERE id = 1`).Scan(&v); err != nil {
			t.Fatalf("内层查询失败（连接池饿死？）: %v", err)
		}
		if v != "z" {
			t.Fatalf("内层查询结果不对: %q", v)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("外层应当遍历 2 行，实际 %d", count)
	}
}
