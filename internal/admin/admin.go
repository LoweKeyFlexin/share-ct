// Package admin serves the private review page at /admin: a read-only list of player
// reports. It is OFF until Cloudflare Access protects the path and the team domain and
// AUD tag below are filled in; until then, and for any request without a valid Access
// login token, /admin answers exactly like any unknown path (404).
package admin

import (
	"database/sql"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/httpx"
)

// The Cloudflare Access application that protects /admin. Neither value is a secret:
// tokens are verified against the team's published public keys. Empty = /admin is off.
const (
	AccessTeamDomain = ""
	AccessAudience   = ""
)

// MaxRows caps one page; reports are few, and the newest come first.
const MaxRows = 500

type Module struct {
	db     *sql.DB
	log    *slog.Logger
	access *AccessVerifier
}

func New(log *slog.Logger, db *sql.DB, access *AccessVerifier) *Module {
	if access == nil {
		access = &AccessVerifier{TeamDomain: AccessTeamDomain, Audience: AccessAudience}
	}
	return &Module{db: db, log: log, access: access}
}

func (m *Module) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", m.page)
	mux.HandleFunc("GET /admin/{$}", m.page)
}

type row struct {
	Reported       string
	Reason         string
	State          string
	Reporter       string
	ReporterSource string
	Target         string
	TargetRef      string
}

func (m *Module) page(w http.ResponseWriter, r *http.Request) {
	if m.access.Verify(r.Context(), r) != nil {
		httpx.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	rows, err := m.reports(r)
	if err != nil {
		m.log.Error("admin: list reports", "error", err.Error())
		httpx.WriteError(w, http.StatusInternalServerError, "internal")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	if err := pageTemplate.Execute(w, struct {
		Rows   []row
		Capped bool
	}{rows, len(rows) == MaxRows}); err != nil {
		m.log.Error("admin: render", "error", err.Error())
	}
}

func (m *Module) reports(r *http.Request) ([]row, error) {
	const q = `
SELECT r.created_at, r.reason, r.state,
       rep.display_name, rep.platform, tgt.display_name, tgt.player_ref
FROM reports r
JOIN players rep ON rep.id = r.reporter_id
JOIN players tgt ON tgt.id = r.target_id
ORDER BY r.created_at DESC, r.id
LIMIT ?`
	res, err := m.db.QueryContext(r.Context(), q, MaxRows)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	var out []row
	for res.Next() {
		var at int64
		var x row
		if err := res.Scan(&at, &x.Reason, &x.State, &x.Reporter, &x.ReporterSource, &x.Target, &x.TargetRef); err != nil {
			return nil, err
		}
		x.Reported = time.Unix(at, 0).UTC().Format("2006-01-02 15:04 UTC")
		if x.Reporter == "" {
			x.Reporter = "(no name)"
		}
		if x.Target == "" {
			x.Target = "(no name)"
		}
		out = append(out, x)
	}
	return out, res.Err()
}

var pageTemplate = template.Must(template.New("admin").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>Share CT reports</title>
<style>
  body { margin:0; padding:16px; background:#1A1325; color:#F2E9FF; font:15px/1.4 -apple-system, system-ui, sans-serif; }
  h1 { font-size:1rem; letter-spacing:.12em; text-transform:uppercase; color:#7CF2A6; margin:0 0 4px; }
  p { color:#A284C0; margin:0 0 16px; font-size:.85rem; }
  .wrap { overflow-x:auto; }
  table { border-collapse:collapse; width:100%; min-width:640px; }
  th, td { text-align:left; padding:8px 10px; border-bottom:1px solid #54346E; vertical-align:top; }
  th { color:#A284C0; font-size:.75rem; letter-spacing:.1em; text-transform:uppercase; }
  td.ref { font-family:ui-monospace, Menlo, monospace; font-size:.75rem; color:#A284C0; overflow-wrap:anywhere; }
  .pending { color:#FFB454; } .dismissed { color:#A284C0; } .upheld { color:#FF6B6B; }
  .empty { color:#A284C0; padding:24px 0; }
</style></head><body>
<h1>Player reports</h1>
<p>Newest first. Read-only. Nothing here is public.{{if .Capped}} Showing the newest 500.{{end}}</p>
{{if .Rows}}<div class="wrap"><table>
<tr><th>Reported</th><th>Reason</th><th>State</th><th>Reported by</th><th>Reported player</th><th>Player ref</th></tr>
{{range .Rows}}<tr><td>{{.Reported}}</td><td>{{.Reason}}</td><td class="{{.State}}">{{.State}}</td><td>{{.Reporter}} <span class="ref">{{.ReporterSource}}</span></td><td>{{.Target}}</td><td class="ref">{{.TargetRef}}</td></tr>
{{end}}</table></div>{{else}}<div class="empty">No reports yet.</div>{{end}}
</body></html>
`))
