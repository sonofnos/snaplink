package handlers

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sonofnos/snaplink/internal/store"
)

type linkView struct {
	Code      string     `json:"code"`
	ShortURL  string     `json:"short_url"`
	LongURL   string     `json:"long_url"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Clicks    int64      `json:"clicks"`
}

func (h *Handler) view(l store.Link) linkView {
	return linkView{Code: l.Code, ShortURL: h.baseURL + "/" + l.Code, LongURL: l.LongURL, CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt, Clicks: l.Clicks}
}

func (h *Handler) ListMyLinks(w http.ResponseWriter, r *http.Request) {
	u, ok := h.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	links, err := h.db.ListLinksByUser(r.Context(), u.ID, 100)
	if err != nil {
		h.logger.Error("list links failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]linkView, 0, len(links))
	for _, l := range links {
		out = append(out, h.view(l))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) DeleteMyLink(w http.ResponseWriter, r *http.Request) {
	u, ok := h.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	code := chi.URLParam(r, "code")
	deleted, err := h.db.DeleteLink(r.Context(), u.ID, code)
	if err != nil {
		h.logger.Error("delete link failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "link not found")
		return
	}
	_ = h.rdb.DelURL(r.Context(), code)
	h.local.Delete(code) // other instances' local copies age out within the local TTL
	w.WriteHeader(http.StatusNoContent)
}

type bucket struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

type analyticsResponse struct {
	Link      linkView `json:"link"`
	Days      int      `json:"days"`
	Total     int64    `json:"total"`
	Uniques   int64    `json:"uniques"`
	Daily     []day    `json:"daily"`
	Browsers  []bucket `json:"browsers"`
	Systems   []bucket `json:"systems"`
	Referrers []bucket `json:"referrers"`
}

type day struct {
	Date   string `json:"date"`
	Clicks int64  `json:"clicks"`
}

func (h *Handler) LinkAnalytics(w http.ResponseWriter, r *http.Request) {
	u, ok := h.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	link, err := h.db.GetLinkByCode(r.Context(), chi.URLParam(r, "code"))
	if err != nil || link.UserID == nil || *link.UserID != u.ID {
		writeError(w, http.StatusNotFound, "link not found") // same answer for "not yours" as "doesn't exist"
		return
	}
	const days = 30
	a, err := h.db.LinkAnalytics(r.Context(), link.Code, days)
	if err != nil {
		h.logger.Error("analytics failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := analyticsResponse{Link: h.view(link), Days: days, Total: a.Total, Uniques: a.Uniques}
	for _, d := range a.Daily {
		resp.Daily = append(resp.Daily, day{Date: d.Day.Format("2006-01-02"), Clicks: d.Clicks})
	}
	browsers, systems, refs := map[string]int64{}, map[string]int64{}, map[string]int64{}
	for _, c := range a.UserAgent {
		b, o := classifyUA(c.Label)
		browsers[b] += c.Count
		systems[o] += c.Count
	}
	for _, c := range a.Referrers {
		refs[referrerHost(c.Label)] += c.Count
	}
	resp.Browsers, resp.Systems, resp.Referrers = top(browsers, 6), top(systems, 6), top(refs, 6)
	writeJSON(w, http.StatusOK, resp)
}

func top(m map[string]int64, n int) []bucket {
	out := make([]bucket, 0, len(m))
	for k, v := range m {
		out = append(out, bucket{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func referrerHost(ref string) string {
	if ref == "" {
		return "Direct"
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return "Direct"
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// classifyUA is deliberately coarse: order matters because Chrome UAs
// contain "Safari" and Edge/Opera UAs contain "Chrome".
func classifyUA(ua string) (browser, system string) {
	l := strings.ToLower(ua)
	switch {
	case ua == "":
		return "Unknown", "Unknown"
	case strings.Contains(l, "bot"), strings.Contains(l, "spider"), strings.Contains(l, "crawl"), strings.Contains(l, "k6/"), strings.Contains(l, "curl"):
		browser = "Bot / CLI"
	case strings.Contains(l, "edg/"):
		browser = "Edge"
	case strings.Contains(l, "opr/"), strings.Contains(l, "opera"):
		browser = "Opera"
	case strings.Contains(l, "firefox"):
		browser = "Firefox"
	case strings.Contains(l, "chrome"), strings.Contains(l, "crios"):
		browser = "Chrome"
	case strings.Contains(l, "safari"):
		browser = "Safari"
	default:
		browser = "Other"
	}
	switch {
	case strings.Contains(l, "iphone"), strings.Contains(l, "ipad"), strings.Contains(l, "ios"):
		system = "iOS"
	case strings.Contains(l, "android"):
		system = "Android"
	case strings.Contains(l, "windows"):
		system = "Windows"
	case strings.Contains(l, "mac os"), strings.Contains(l, "macintosh"):
		system = "macOS"
	case strings.Contains(l, "linux"), strings.Contains(l, "x11"):
		system = "Linux"
	default:
		system = "Other"
	}
	return
}
