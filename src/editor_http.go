package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// previewShell is loaded in the editor's preview iframe. Its CSP sandbox gives it an opaque origin, so the page being
// previewed runs its own scripts and styles but can never act as the admin: no cookies, and its requests carry
// "Origin: null", which every write refuses. The editor posts the HTML here and the shell replaces itself with it.
const previewShell = `<!doctype html><meta charset="utf-8"><script>
addEventListener("message", e => {
  if (e.source !== parent || typeof e.data !== "string") return;
  document.open(); document.write(e.data); document.close();
}, { once: true });
parent.postMessage("preview-ready", "*");
</script>`

const previewCSP = "sandbox allow-scripts allow-forms allow-modals allow-popups; " +
	"default-src * data: blob: 'unsafe-inline' 'unsafe-eval'; frame-ancestors 'self'"

func addEditorRoutes(mux *http.ServeMux, store *Store, auth *Auth, maxUpload int64) {
	mux.HandleFunc("GET /preview", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", previewCSP)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(previewShell))
	})
	mux.Handle("GET /api/pages/{name}/source", auth.Require(func(w http.ResponseWriter, r *http.Request, id string) {
		b, m, err := store.Source(id, r.PathValue("name"))
		if err != nil {
			fail(w, err)
			return
		}
		// served as inert text: opening this URL directly can never run the page as the admin
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("X-Page-Updated", m.Updated.Format(time.RFC3339Nano))
		_, _ = w.Write(b)
	}))
	mux.Handle("GET /api/drafts", auth.Require(func(w http.ResponseWriter, _ *http.Request, id string) {
		list, err := store.Drafts(id)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, list)
	}))
	mux.Handle("GET /api/drafts/{name}", auth.Require(func(w http.ResponseWriter, r *http.Request, id string) {
		d, err := store.GetDraft(id, r.PathValue("name"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, d)
	}))
	mux.Handle("PUT /api/drafts/{name}", auth.Require(func(w http.ResponseWriter, r *http.Request, id string) {
		var d Draft
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUpload)).Decode(&d); err != nil {
			http.Error(w, `body must be JSON {"html": "...", "base": "<page updated time>"}`, http.StatusBadRequest)
			return
		}
		d.Name = r.PathValue("name")
		d, err := store.SaveDraft(id, d)
		if err != nil {
			fail(w, err)
			return
		}
		d.HTML = ""
		writeJSON(w, d)
	}))
	mux.Handle("DELETE /api/drafts/{name}", auth.Require(func(w http.ResponseWriter, r *http.Request, id string) {
		if err := store.DeleteDraft(id, r.PathValue("name")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.Handle("POST /api/drafts/{name}/publish", auth.Require(func(w http.ResponseWriter, r *http.Request, id string) {
		m, err := store.Publish(id, r.PathValue("name"), r.URL.Query().Get("force") == "1")
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, m)
	}))
	mux.Handle("POST /api/favicon", auth.Require(func(w http.ResponseWriter, r *http.Request, _ string) {
		var in struct{ HTML, Href string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUpload)).Decode(&in); err != nil {
			http.Error(w, `body must be JSON {"html": "...", "href": "data:image/..."}`, http.StatusBadRequest)
			return
		}
		out, err := SetFavicon([]byte(in.HTML), in.Href)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, map[string]string{"html": string(out)})
	}))
}
