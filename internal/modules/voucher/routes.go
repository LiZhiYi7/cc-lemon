package voucher

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/lizhiyi7/cc-lemon/internal/kernel"
	"github.com/lizhiyi7/cc-lemon/internal/modules/book"
	"github.com/lizhiyi7/cc-lemon/internal/platform/httpx"
)

func (s *Service) Routes(r chi.Router) {
	bk := func(r *http.Request) *book.Book { return book.From(r.Context()).Book }

	r.Get("/vouchers", httpx.Handle(func(r *http.Request) (any, error) {
		q := r.URL.Query()
		f := Filter{Word: q.Get("word"), Status: q.Get("status"), Keyword: q.Get("keyword"),
			AccountID: httpx.QueryInt64(r, "account_id"), Page: httpx.QueryInt(r, "page", 1), PageSize: httpx.QueryInt(r, "page_size", 50)}
		var err error
		if f.From, err = kernel.ParsePeriod(q.Get("from")); err != nil {
			return nil, httpx.BadRequest(err.Error())
		}
		if f.To, err = kernel.ParsePeriod(q.Get("to")); err != nil {
			return nil, httpx.BadRequest(err.Error())
		}
		return s.List(r.Context(), bk(r).ID, f)
	}))
	r.Get("/vouchers/next-number", httpx.Handle(func(r *http.Request) (any, error) {
		date, err := time.Parse(time.DateOnly, r.URL.Query().Get("date"))
		if err != nil {
			return nil, httpx.BadRequest("日期无效")
		}
		n, err := s.NextNumber(r.Context(), bk(r).ID, kernel.Period{Year: date.Year(), Month: int(date.Month())}, r.URL.Query().Get("word"))
		return map[string]int{"number": n}, err
	}))
	r.Get("/vouchers/{voucherID}", httpx.Handle(func(r *http.Request) (any, error) {
		id, err := httpx.ParamInt64(r, "voucherID")
		if err != nil {
			return nil, err
		}
		return s.Get(r.Context(), bk(r).ID, id)
	}))
	r.Post("/vouchers/parallel-suggest", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[struct {
			Lines []LineInput `json:"lines"`
		}](r)
		if err != nil {
			return nil, err
		}
		return s.SuggestParallel(r.Context(), bk(r), in.Lines)
	}))
	r.Get("/parallel-rules", httpx.Handle(func(r *http.Request) (any, error) {
		return s.ParallelRules(r.Context(), bk(r).ID)
	}))

	r.Group(func(r chi.Router) {
		r.Use(book.Require(PermWrite))
		r.Post("/vouchers", httpx.Handle(func(r *http.Request) (any, error) {
			in, err := httpx.Bind[Input](r)
			if err != nil {
				return nil, err
			}
			return s.Save(r.Context(), bk(r), 0, in, SourceManual)
		}))
		r.Put("/vouchers/{voucherID}", httpx.Handle(func(r *http.Request) (any, error) {
			id, err := httpx.ParamInt64(r, "voucherID")
			if err != nil {
				return nil, err
			}
			in, err := httpx.Bind[Input](r)
			if err != nil {
				return nil, err
			}
			// 修改不改变凭证来源，传入值仅在新增时使用。
			return s.Save(r.Context(), bk(r), id, in, SourceManual)
		}))
		r.Delete("/vouchers/{voucherID}", httpx.Handle(func(r *http.Request) (any, error) {
			id, err := httpx.ParamInt64(r, "voucherID")
			if err != nil {
				return nil, err
			}
			return nil, s.Delete(r.Context(), bk(r), id)
		}))
		r.Post("/vouchers/renumber", httpx.Handle(func(r *http.Request) (any, error) {
			in, err := httpx.Bind[kernel.Period](r)
			if err != nil {
				return nil, err
			}
			return nil, s.Renumber(r.Context(), bk(r), in)
		}))
	})

	r.With(book.Require(PermReview)).Post("/vouchers/review", httpx.Handle(func(r *http.Request) (any, error) {
		in, err := httpx.Bind[struct {
			IDs      []int64 `json:"ids"`
			Reviewed bool    `json:"reviewed"`
		}](r)
		if err != nil {
			return nil, err
		}
		return nil, s.SetReviewed(r.Context(), bk(r), in.IDs, in.Reviewed)
	}))

	r.Group(func(r chi.Router) {
		r.Use(book.Require(PermParallelWrite))
		r.Post("/parallel-rules", httpx.Handle(func(r *http.Request) (any, error) {
			in, err := httpx.Bind[ParallelRule](r)
			if err != nil {
				return nil, err
			}
			return s.SaveParallelRule(r.Context(), bk(r), in)
		}))
		r.Delete("/parallel-rules/{ruleID}", httpx.Handle(func(r *http.Request) (any, error) {
			id, err := httpx.ParamInt64(r, "ruleID")
			if err != nil {
				return nil, err
			}
			return nil, s.DeleteParallelRule(r.Context(), bk(r).ID, id)
		}))
	})
}
