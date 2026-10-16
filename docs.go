package typed

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sync"
)

const docsPage = `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
  <script>SwaggerUIBundle({ url: %s, dom_id: "#swagger-ui" });</script>
</body>
</html>
`

// mountDocs registers the spec and documentation endpoints on the router.
func mountDocs(a *API) {
	cfg := a.s.cfg

	if cfg.SpecPath != "-" {
		var (
			mu     sync.Mutex
			cached []byte
		)
		// The document is built on first request, after routes are
		// registered. A failed marshal is not cached.
		specJSON := func() ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			if cached != nil {
				return cached, nil
			}
			spec := a.OpenAPI()
			a.s.mu.Lock()
			b, err := json.Marshal(spec)
			a.s.mu.Unlock()
			if err != nil {
				return nil, err
			}
			cached = b
			return b, nil
		}
		a.router.Get(cfg.SpecPath, func(w http.ResponseWriter, r *http.Request) {
			b, err := specJSON()
			if err != nil {
				a.handleError(w, r, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
		})
	}

	if cfg.DocsPath != "-" {
		specURL, err := json.Marshal(cfg.SpecPath)
		if err != nil {
			panic("typed: " + err.Error())
		}
		page := []byte(fmt.Sprintf(docsPage, html.EscapeString(cfg.Title), specURL))
		a.router.Get(cfg.DocsPath, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(page)
		})
	}
}
