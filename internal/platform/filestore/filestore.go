// Package filestore 存放凭证附件的图片。
//
// 不建数据表：每张凭证一个目录，目录里放原图、缩略图和一个 index.json 记录
// 文件名、大小、上传人与时间。删除凭证时整个目录一起删掉。
//
// 约束：调用方负责并发（凭证模块传图时会先锁账套行），这里只保证
// 单次写入不会留下半截文件——先写临时文件，落盘后改名。
package filestore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 暴露给上层的错误，handler 据此决定 HTTP 状态码。
var (
	ErrDisabled  = errors.New("附件存储不可用")
	ErrTooMany   = errors.New("附件数量已达上限")
	ErrNotFound  = errors.New("附件不存在")
	ErrBadID     = errors.New("附件标识非法")
	ErrBrokenIdx = errors.New("附件索引损坏")
)

// ErrTooLarge 单张图片超过上限。
var ErrTooLarge = errors.New("图片超出大小上限")

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Exts 允许的图片类型 → 落盘扩展名。拒绝 SVG：它能带脚本，等同于存储型 XSS。
var Exts = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// Item 与 index.json 里的一条记录同构，直接返回给前端。
type Item struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	File           string `json:"file"`
	Thumb          string `json:"thumb,omitempty"`
	Mime           string `json:"mime"`
	Size           int64  `json:"size"`
	UploadedBy     int64  `json:"uploaded_by"`
	UploadedByName string `json:"uploaded_by_name"`
	UploadedAt     string `json:"uploaded_at"`
}

// NewItem 一次上传的入参。Data 由调用方保证长度不超过 Store 的上限校验。
type NewItem struct {
	Name     string
	Mime     string
	Data     io.Reader
	Thumb    []byte
	UserID   int64
	UserName string
	Now      time.Time
}

type index struct {
	Version   int    `json:"version"`
	VoucherID int64  `json:"voucher_id"`
	Items     []Item `json:"items"`
}

// Store 附件仓储。root 为附件根目录（其下按 账套/凭证 分目录）。
type Store struct {
	root     string
	maxFile  int64
	maxFiles int
	disabled bool
}

// New 创建仓储。目录不可写时不返回错误，而是把 Store 标记为不可用：
// 附件功能缺失不应该让整个记账系统起不来。
func New(root string, maxFile int64, maxFiles int) *Store {
	s := &Store{root: root, maxFile: maxFile, maxFiles: maxFiles}
	if root == "" {
		s.disabled = true
		return s
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		s.disabled = true
		return s
	}
	s.root = abs
	if err := os.MkdirAll(filepath.Join(s.root, "attachments"), 0o755); err != nil {
		s.disabled = true
		return s
	}
	// 写探针：容器里目录存在但属主不对时，只有真正写一次才能发现。
	probe := filepath.Join(s.root, "attachments", ".probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		s.disabled = true
		return s
	}
	_ = os.Remove(probe)
	return s
}

// Enabled 附件功能是否可用。
func (s *Store) Enabled() bool { return s != nil && !s.disabled }

// MaxFile 单张图片大小上限（字节）。
func (s *Store) MaxFile() int64 {
	if s == nil {
		return 0
	}
	return s.maxFile
}

// Root 返回附件根目录，供启动日志与排障使用。
func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Store) dir(bookID, voucherID int64) (string, error) {
	if bookID <= 0 || voucherID <= 0 {
		return "", ErrBadID
	}
	dir := filepath.Join(s.root, "attachments", fmt.Sprint(bookID), fmt.Sprint(voucherID))
	// 双重保险：拼出来的路径必须仍在根目录下。
	if !strings.HasPrefix(dir, filepath.Join(s.root, "attachments")+string(os.PathSeparator)) {
		return "", ErrBadID
	}
	return dir, nil
}

func (s *Store) indexPath(bookID, voucherID int64) (string, string, error) {
	dir, err := s.dir(bookID, voucherID)
	if err != nil {
		return "", "", err
	}
	return dir, filepath.Join(dir, "index.json"), nil
}

func (s *Store) readIndex(bookID, voucherID int64) (index, string, error) {
	dir, ipath, err := s.indexPath(bookID, voucherID)
	if err != nil {
		return index{}, "", err
	}
	idx := index{Version: 1, VoucherID: voucherID, Items: []Item{}}
	raw, err := os.ReadFile(ipath)
	if errors.Is(err, os.ErrNotExist) {
		return idx, dir, nil
	}
	if err != nil {
		return index{}, dir, err
	}
	if len(raw) == 0 {
		return idx, dir, nil
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		// 不静默当成空目录：索引损坏意味着有人手工动过文件，宁可报错也别把图片“弄丢”。
		return index{}, dir, fmt.Errorf("%w: %v", ErrBrokenIdx, err)
	}
	if idx.Items == nil {
		idx.Items = []Item{}
	}
	return idx, dir, nil
}

func (s *Store) writeIndex(dir string, idx index) error {
	idx.Version = 1
	raw, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".index.json.tmp")
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return renameRetry(tmp, filepath.Join(dir, "index.json"))
}

// List 列出某张凭证的附件。目录不存在返回空列表（尚未传过图）。
func (s *Store) List(bookID, voucherID int64) ([]Item, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	idx, _, err := s.readIndex(bookID, voucherID)
	if err != nil {
		return nil, err
	}
	return idx.Items, nil
}

// Append 保存一张图片并登记到索引。
func (s *Store) Append(bookID, voucherID int64, in NewItem) (Item, error) {
	if !s.Enabled() {
		return Item{}, ErrDisabled
	}
	ext, ok := Exts[in.Mime]
	if !ok {
		return Item{}, fmt.Errorf("不支持的图片类型：%s", in.Mime)
	}
	idx, dir, err := s.readIndex(bookID, voucherID)
	if err != nil {
		return Item{}, err
	}
	if len(idx.Items) >= s.maxFiles {
		return Item{}, ErrTooMany
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Item{}, err
	}

	id, err := newID()
	if err != nil {
		return Item{}, err
	}

	file, err := writeBlob(dir, id+ext, in.Data, s.maxFile)
	if err != nil {
		return Item{}, err
	}

	item := Item{
		ID:             id,
		Name:           in.Name,
		File:           file,
		Mime:           in.Mime,
		Size:           0,
		UploadedBy:     in.UserID,
		UploadedByName: in.UserName,
		UploadedAt:     in.Now.Format(time.RFC3339),
	}
	if st, err := os.Stat(filepath.Join(dir, file)); err == nil {
		item.Size = st.Size()
	}

	if len(in.Thumb) > 0 {
		if name, err := writeBytes(dir, id+".thumb.jpg", in.Thumb); err == nil {
			item.Thumb = name
		}
	}

	idx.Items = append(idx.Items, item)
	if err := s.writeIndex(dir, idx); err != nil {
		// 索引没写成，把刚落的文件删掉，避免出现“盘上有图但列表看不见”。
		_ = os.Remove(filepath.Join(dir, item.File))
		if item.Thumb != "" {
			_ = os.Remove(filepath.Join(dir, item.Thumb))
		}
		return Item{}, err
	}
	return item, nil
}

// Open 打开某张图（thumb=true 时优先返回缩略图，没有则回落到原图）。
func (s *Store) Open(bookID, voucherID int64, id string, thumb bool) (*os.File, Item, error) {
	if !s.Enabled() {
		return nil, Item{}, ErrDisabled
	}
	if !idPattern.MatchString(id) {
		return nil, Item{}, ErrBadID
	}
	idx, dir, err := s.readIndex(bookID, voucherID)
	if err != nil {
		return nil, Item{}, err
	}
	for _, it := range idx.Items {
		if it.ID != id {
			continue
		}
		name := it.File
		if thumb && it.Thumb != "" {
			name = it.Thumb
		}
		// index.json 是本地文件，仍按白名单校验一遍文件名，杜绝 ../ 之类的写法。
		if name != filepath.Base(name) {
			return nil, Item{}, ErrBadID
		}
		f, err := os.Open(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			return nil, Item{}, ErrNotFound
		}
		if err != nil {
			return nil, Item{}, err
		}
		return f, it, nil
	}
	return nil, Item{}, ErrNotFound
}

// Remove 删除一张图：先改索引再删文件。
// 反过来的话中途失败会留下“索引指向已删文件”的坏记录。
func (s *Store) Remove(bookID, voucherID int64, id string) (Item, error) {
	if !s.Enabled() {
		return Item{}, ErrDisabled
	}
	if !idPattern.MatchString(id) {
		return Item{}, ErrBadID
	}
	idx, dir, err := s.readIndex(bookID, voucherID)
	if err != nil {
		return Item{}, err
	}
	kept := make([]Item, 0, len(idx.Items))
	var found *Item
	for i := range idx.Items {
		if idx.Items[i].ID == id {
			found = &idx.Items[i]
			continue
		}
		kept = append(kept, idx.Items[i])
	}
	if found == nil {
		return Item{}, ErrNotFound
	}
	idx.Items = kept
	if err := s.writeIndex(dir, idx); err != nil {
		return Item{}, err
	}
	if f := found.File; f != "" && f == filepath.Base(f) {
		_ = os.Remove(filepath.Join(dir, f))
	}
	if t := found.Thumb; t != "" && t == filepath.Base(t) {
		_ = os.Remove(filepath.Join(dir, t))
	}
	return *found, nil
}

// RemoveVoucher 删除凭证时清空它的附件目录（凭证已被删除，孤儿文件没有意义）。
func (s *Store) RemoveVoucher(bookID, voucherID int64) error {
	if !s.Enabled() {
		return nil
	}
	dir, err := s.dir(bookID, voucherID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func newID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func writeBlob(dir, name string, r io.Reader, limit int64) (string, error) {
	tmp := filepath.Join(dir, ".part-"+name)
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	// 多读 1 字节：能区分“正好等于上限”和“超过上限”。
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if n > limit {
		os.Remove(tmp)
		return "", ErrTooLarge
	}
	if n == 0 {
		os.Remove(tmp)
		return "", errors.New("图片内容为空")
	}
	if err := renameRetry(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, nil
}

func writeBytes(dir, name string, b []byte) (string, error) {
	tmp := filepath.Join(dir, ".part-"+name)
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", err
	}
	if err := renameRetry(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, nil
}

// renameRetry 在 Windows 上偶发被杀毒软件占用句柄导致改名失败，重试几次即可。
func renameRetry(from, to string) error {
	var err error
	for i := 0; i < 3; i++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}
