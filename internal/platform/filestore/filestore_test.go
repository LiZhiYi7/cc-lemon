package filestore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := New(t.TempDir(), 1<<20, 3)
	if !s.Enabled() {
		t.Fatal("测试用的附件目录应当可写")
	}
	return s
}

func add(t *testing.T, s *Store, name string) Item {
	t.Helper()
	it, err := s.Append(1, 2, NewItem{
		Name: name, Mime: "image/png", Data: bytes.NewReader([]byte("PNGDATA")),
		UserID: 7, UserName: "张三", Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("追加附件失败: %v", err)
	}
	return it
}

func TestAppendListOpenRemove(t *testing.T) {
	s := newTestStore(t)

	it := add(t, s, "发票.png")
	if len(it.ID) != 32 {
		t.Fatalf("ID 应为 32 位十六进制，实际 %q", it.ID)
	}
	if it.Size != 7 {
		t.Fatalf("大小应为 7，实际 %d", it.Size)
	}

	list, err := s.List(1, 2)
	if err != nil || len(list) != 1 {
		t.Fatalf("列表应有 1 条，实际 %d 条（err=%v）", len(list), err)
	}

	f, got, err := s.Open(1, 2, it.ID, false)
	if err != nil {
		t.Fatalf("打开附件失败: %v", err)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(f)
	f.Close()
	if buf.String() != "PNGDATA" || got.ID != it.ID {
		t.Fatalf("取回内容不一致: %q", buf.String())
	}

	if _, err := s.Remove(1, 2, it.ID); err != nil {
		t.Fatalf("删除附件失败: %v", err)
	}
	if list, _ = s.List(1, 2); len(list) != 0 {
		t.Fatalf("删除后应为空，实际 %d 条", len(list))
	}
	if _, _, err := s.Open(1, 2, it.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("已删除的附件应返回 ErrNotFound，实际 %v", err)
	}
}

func TestRejectsUnsupportedTypeAndBadID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Append(1, 2, NewItem{Mime: "image/svg+xml", Data: bytes.NewReader([]byte("<svg/>"))}); err == nil {
		t.Fatal("SVG 必须被拒绝")
	}
	if _, _, err := s.Open(1, 2, "../../etc/passwd", false); !errors.Is(err, ErrBadID) {
		t.Fatalf("非法 ID 应返回 ErrBadID，实际 %v", err)
	}
	if _, _, err := s.Open(1, 2, "0123456789abcdef0123456789abcdef", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的附件应返回 ErrNotFound，实际 %v", err)
	}
}

func TestMaxFiles(t *testing.T) {
	s := newTestStore(t) // 上限 3 张
	for i := 0; i < 3; i++ {
		add(t, s, "a.png")
	}
	if _, err := s.Append(1, 2, NewItem{Name: "x", Mime: "image/png", Data: bytes.NewReader([]byte("x"))}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("超出上限应返回 ErrTooMany，实际 %v", err)
	}
}

func TestTooLarge(t *testing.T) {
	s := New(t.TempDir(), 4, 10)
	_, err := s.Append(1, 2, NewItem{Name: "big.png", Mime: "image/png", Data: bytes.NewReader([]byte("12345"))})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("超过单张上限应返回 ErrTooLarge，实际 %v", err)
	}
}

func TestBrokenIndexIsReported(t *testing.T) {
	s := newTestStore(t)
	add(t, s, "a.png")
	dir, err := s.dir(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("{不是 JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(1, 2); !errors.Is(err, ErrBrokenIdx) {
		t.Fatalf("索引损坏应返回 ErrBrokenIdx，实际 %v", err)
	}
}

func TestRemoveVoucherCleansDir(t *testing.T) {
	s := newTestStore(t)
	add(t, s, "a.png")
	dir, _ := s.dir(1, 2)
	if err := s.RemoveVoucher(1, 2); err != nil {
		t.Fatalf("清理凭证目录失败: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("目录应已删除，Stat 返回 %v", err)
	}
	// 再次清理不应报错（幂等）
	if err := s.RemoveVoucher(1, 2); err != nil {
		t.Fatalf("重复清理应幂等，实际 %v", err)
	}
}

func TestDisabledWhenUnwritable(t *testing.T) {
	// 根目录指向一个文件而不是目录，必然创建失败。
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(f, 1<<20, 10)
	if s.Enabled() {
		t.Fatal("路径不可用时 Store 应为禁用状态")
	}
	if _, err := s.List(1, 2); !errors.Is(err, ErrDisabled) {
		t.Fatalf("禁用状态应返回 ErrDisabled，实际 %v", err)
	}
}
