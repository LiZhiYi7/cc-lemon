package voucher

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/lizhiyi7/yanghe-finance/internal/modules/book"
	"github.com/lizhiyi7/yanghe-finance/internal/platform/audit"
	"github.com/lizhiyi7/yanghe-finance/internal/platform/auth"
	"github.com/lizhiyi7/yanghe-finance/internal/platform/filestore"
	"github.com/lizhiyi7/yanghe-finance/internal/platform/httpx"
)

const (
	// maxUploadBytes 单次请求体上限（原图 + 缩略图 + multipart 边界）。
	maxUploadBytes = 12 << 20
	// maxThumbBytes 缩略图上限，正常在 100KB 以内。
	maxThumbBytes = 512 << 10
)

func attachErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, filestore.ErrDisabled):
		return httpx.Errf(http.StatusServiceUnavailable, "attachment_unavailable",
			"附件存储不可用，请检查附件目录是否存在、进程是否有写入权限")
	case errors.Is(err, filestore.ErrTooMany):
		return httpx.Conflict("这张凭证的图片数量已达上限")
	case errors.Is(err, filestore.ErrTooLarge):
		return httpx.Errf(http.StatusRequestEntityTooLarge, "attachment_too_large", "图片超出大小上限")
	case errors.Is(err, filestore.ErrNotFound):
		return httpx.NotFound("图片不存在")
	case errors.Is(err, filestore.ErrBadID):
		return httpx.BadRequest("图片标识非法")
	case errors.Is(err, filestore.ErrBrokenIdx):
		return httpx.Errf(http.StatusInternalServerError, "attachment_index_broken",
			"附件索引文件损坏，请检查附件目录或联系管理员")
	}
	return err
}

// withEditableVoucher 复用凭证保存的同一把账套锁，保证“凭证是否可改”的判断
// 与附件写入之间不会被并发修改打断。
func (s *Service) withEditableVoucher(ctx context.Context, b *book.Book, voucherID int64, fn func(ctx context.Context, h *header) error) error {
	if s.files == nil || !s.files.Enabled() {
		return filestore.ErrDisabled
	}
	return s.db.Tx(ctx, func(ctx context.Context) error {
		if err := s.books.Lock(ctx, b.ID); err != nil {
			return err
		}
		h, err := s.header(ctx, b.ID, voucherID)
		if err != nil {
			return err
		}
		if h.status == StatusReviewed {
			return httpx.Conflict("凭证已审核，请先取消审核")
		}
		if err := s.books.EnsureOpen(ctx, b, h.period); err != nil {
			return err
		}
		return fn(ctx, h)
	})
}

// listAttachments GET /vouchers/{voucherID}/attachments
// 只读接口：账套成员即可查看（与“能看凭证”一致）。存储未启用时返回空列表，
// 界面据此隐藏上传入口，不必弹错误。
func (s *Service) listAttachments(w http.ResponseWriter, r *http.Request) {
	b := book.From(r.Context()).Book
	voucherID, err := httpx.ParamInt64(r, "voucherID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items, err := s.files.List(b.ID, voucherID)
	if err != nil {
		if errors.Is(err, filestore.ErrDisabled) {
			httpx.JSON(w, http.StatusOK, []filestore.Item{})
			return
		}
		httpx.WriteError(w, r, attachErr(err))
		return
	}
	if items == nil {
		items = []filestore.Item{}
	}
	httpx.JSON(w, http.StatusOK, items)
}

// uploadAttachment POST /vouchers/{voucherID}/attachments（multipart）
// 字段：file（必填，jpg/png/webp）、thumb（可选，前端生成的 JPEG 缩略图）。
func (s *Service) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	b := book.From(r.Context()).Book
	voucherID, err := httpx.ParamInt64(r, "voucherID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	// httpx.Bind 只解 JSON 且限制 8MiB，图片必须自己卡一遍请求体。
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		httpx.WriteError(w, r, httpx.BadRequest("图片上传失败：请求体过大或格式不正确"))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, r, httpx.BadRequest("没有收到图片文件"))
		return
	}
	defer file.Close()

	// 只认文件头，不认扩展名与客户端声明的 Content-Type。
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		httpx.WriteError(w, r, httpx.BadRequest("读取图片失败"))
		return
	}
	mime := http.DetectContentType(head[:n])
	if _, ok := filestore.Exts[mime]; !ok {
		httpx.WriteError(w, r, httpx.BadRequest("只支持 JPG / PNG / WebP 图片"))
		return
	}
	if hdr.Size > s.maxFileBytes() {
		httpx.WriteError(w, r, attachErr(filestore.ErrTooLarge))
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		httpx.WriteError(w, r, httpx.BadRequest("读取图片失败"))
		return
	}

	thumb, err := readThumb(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	me := auth.CurrentUser(r.Context())
	name := me.DisplayName
	if name == "" {
		name = me.Username
	}

	var saved filestore.Item
	err = s.withEditableVoucher(r.Context(), b, voucherID, func(ctx context.Context, h *header) error {
		item, err := s.files.Append(b.ID, voucherID, filestore.NewItem{
			Name:     tidyFileName(hdr.Filename),
			Mime:     mime,
			Data:     file,
			Thumb:    thumb,
			UserID:   me.ID,
			UserName: name,
			Now:      time.Now(),
		})
		if err != nil {
			return attachErr(err)
		}
		saved = item
		audit.Log(ctx, s.db, b.ID, "voucher.attach.add", "voucher", voucherID, map[string]any{
			"attachment_id": item.ID, "file_name": item.Name, "bytes": item.Size, "mime": item.Mime,
		})
		return nil
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, saved)
}

// serveAttachment GET /vouchers/{voucherID}/attachments/{attachmentID}[?thumb=1]
// 前端用 fetch + blob 取图（JWT 在 Authorization 头里，裸 <img src> 带不上）。
func (s *Service) serveAttachment(w http.ResponseWriter, r *http.Request) {
	b := book.From(r.Context()).Book
	voucherID, err := httpx.ParamInt64(r, "voucherID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id := chi.URLParam(r, "attachmentID")
	thumb := r.URL.Query().Get("thumb") == "1"

	f, item, err := s.files.Open(b.ID, voucherID, id, thumb)
	if err != nil {
		httpx.WriteError(w, r, attachErr(err))
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		httpx.WriteError(w, r, httpx.Errf(http.StatusInternalServerError, "internal", "读取图片失败"))
		return
	}

	w.Header().Set("Content-Type", item.Mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 私有缓存：同一用户在同一会话里重复打开预览不必回源。
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(item.Name))
	http.ServeContent(w, r, item.Name, st.ModTime(), f)
}

// deleteAttachment DELETE /vouchers/{voucherID}/attachments/{attachmentID}
func (s *Service) deleteAttachment(w http.ResponseWriter, r *http.Request) {
	b := book.From(r.Context()).Book
	voucherID, err := httpx.ParamInt64(r, "voucherID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id := chi.URLParam(r, "attachmentID")

	err = s.withEditableVoucher(r.Context(), b, voucherID, func(ctx context.Context, h *header) error {
		item, err := s.files.Remove(b.ID, voucherID, id)
		if err != nil {
			return attachErr(err)
		}
		audit.Log(ctx, s.db, b.ID, "voucher.attach.delete", "voucher", voucherID, map[string]any{
			"attachment_id": item.ID, "file_name": item.Name,
		})
		return nil
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) maxFileBytes() int64 {
	if s.files == nil {
		return 0
	}
	return s.files.MaxFile()
}

// readThumb 读取可选的缩略图字段；体积异常时忽略，界面上回落到原图即可，
// 不值得因为一张缩略图让整次上传失败。
func readThumb(r *http.Request) ([]byte, error) {
	fh, ok := r.MultipartForm.File["thumb"]
	if !ok || len(fh) == 0 {
		return nil, nil
	}
	f, err := fh[0].Open()
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, maxThumbBytes+1))
	if err != nil || len(buf) == 0 || len(buf) > maxThumbBytes {
		return nil, nil
	}
	return buf, nil
}

// tidyFileName 只用于显示：去掉路径与控制字符，长度截断到 120 个字符。
// 落盘文件名一律使用服务端生成的 ID，绝不使用这里的值。
func tidyFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "图片"
	}
	if utf8.RuneCountInString(name) > 120 {
		rs := []rune(name)
		name = string(rs[:120])
	}
	return name
}
