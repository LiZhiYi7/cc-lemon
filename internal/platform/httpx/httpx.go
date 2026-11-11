// Package httpx 提供统一的 JSON 响应、错误模型与请求解析。
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// Error 是面向客户端的业务错误。
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func Errf(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}
func BadRequest(msg string) *Error   { return Errf(http.StatusBadRequest, "bad_request", msg) }
func Unauthorized(msg string) *Error { return Errf(http.StatusUnauthorized, "unauthorized", msg) }
func Forbidden(msg string) *Error    { return Errf(http.StatusForbidden, "forbidden", msg) }
func NotFound(msg string) *Error     { return Errf(http.StatusNotFound, "not_found", msg) }
func Conflict(msg string) *Error     { return Errf(http.StatusConflict, "conflict", msg) }

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		slog.Error("内部错误", "method", r.Method, "path", r.URL.Path, "err", err)
		e = Errf(http.StatusInternalServerError, "internal", "服务器内部错误")
	}
	JSON(w, e.Status, map[string]any{"error": e})
}

// Handle 把 “返回 (结果, 错误)” 的处理函数适配为 http.HandlerFunc。
func Handle(fn func(r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(r)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		if v == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		JSON(w, http.StatusOK, v)
	}
}

// Bind 解析 JSON 请求体。
func Bind[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20))
	if err := dec.Decode(&v); err != nil {
		return v, BadRequest("请求格式错误: " + err.Error())
	}
	return v, nil
}

func ParamInt64(r *http.Request, name string) (int64, error) {
	n, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil {
		return 0, BadRequest("无效的参数 " + name)
	}
	return n, nil
}

func QueryInt64(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	return n
}

func QueryInt(r *http.Request, name string, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil {
		return n
	}
	return def
}
