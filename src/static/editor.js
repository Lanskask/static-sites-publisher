"use strict";
// The page editor: the HTML source on the left, a live preview on the right. Every change is saved as a draft on the
// server a moment after typing stops; only Publish changes the live page. Uses $, api, domain, existing, refresh and
// showError from app.js.

const TEMPLATE = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>New page</title>
  <link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'%3E%3Ctext y='.9em' font-size='90'%3E%F0%9F%93%84%3C/text%3E%3C/svg%3E">
  <style>
    body { font: 18px/1.6 system-ui, sans-serif; max-width: 40rem; margin: 3rem auto; padding: 0 1rem; }
  </style>
</head>
<body>
  <h1>Hello</h1>
  <p>Edit the HTML on the left; the preview updates as you type.</p>
</body>
</html>
`;

// The source box: a plain textarea until CodeMirror (vendor/codemirror.js, loaded on first open) takes over. If that
// file fails to load, the textarea keeps working.
const source = {
  get: () => $("ed-src").value,
  set: (text) => { $("ed-src").value = text; },
};
$("ed-src").addEventListener("input", () => changed());

let codeMirror = null;
function loadCodeMirror() {
  codeMirror ??= import("./vendor/codemirror.js").then(({ EditorView, basicSetup, html, oneDark }) => {
    let quiet = false; // set while the program replaces the text, so that is not taken for an edit
    // In a shadow root CodeMirror styles itself with constructable stylesheets; on the document it would add <style>
    // tags, which the admin's CSP (default-src 'self') blocks. The textarea, now unslotted, is no longer shown.
    const root = $("ed-code").attachShadow({ mode: "open" });
    const view = new EditorView({
      parent: root,
      root,
      doc: $("ed-src").value,
      extensions: [
        basicSetup, html(), EditorView.lineWrapping,
        EditorView.theme({ "&": { flex: "1", minWidth: "0", fontSize: "0.85rem" }, ".cm-scroller": { fontFamily: "var(--mono)" } }),
        ...(matchMedia("(prefers-color-scheme: dark)").matches ? [oneDark] : []),
        EditorView.updateListener.of((u) => { if (u.docChanged && !quiet) changed(); }),
      ],
    });
    source.get = () => view.state.doc.toString();
    source.set = (text) => {
      quiet = true;
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text } });
      quiet = false;
    };
  }).catch((e) => console.warn("CodeMirror did not load; using the plain textarea", e));
  return codeMirror;
}

let ed = null; // {name, base, isNew, saved}: the page being edited
let saveTimer = 0;
let saving = Promise.resolve();

async function openEditor(name, isNew) {
  ed = { name, base: null, isNew, saved: true, visual: false };
  let html = TEMPLATE;
  try {
    if (!name) throw Object.assign(new Error("new page"), { status: 404 });
    const d = await api("GET", `/api/drafts/${name}`);
    html = d.html;
    ed.base = d.base;
    setState(`Draft from ${new Date(d.saved).toLocaleString()}. The live page is unchanged until you publish.`);
  } catch (e) {
    if (e.status !== 404) return showError(e);
    if (!isNew) {
      const res = await fetch(`/api/pages/${name}/source`);
      if (!res.ok) return showError(new Error(await res.text()));
      html = await res.text();
      ed.base = res.headers.get("X-Page-Updated");
      setState("Editing the live page. Changes are kept as a draft until you publish.");
    } else {
      ed.saved = false; // the template is not on the server yet
      setState("New page. Pick a name; the draft is saved as you type.");
    }
  }
  $("ed-title").textContent = name ? (isNew ? `New page: ${name}` : `Edit ${name}`) : "New page";
  $("ed-name-field").hidden = Boolean(name);
  $("ed-name").value = name || "";
  await loadCodeMirror();
  source.set(html);
  updateButtons();
  showPreview();
  $("editor").showModal();
}

function setState(text, error) {
  $("ed-state").textContent = text;
  $("ed-state").className = error ? "error" : "";
}

function updateButtons() {
  $("ed-discard").disabled = !drafts.has(ed.name);
  // Visual editing needs a page without scripts: the editable copy runs none, so a script page would look broken.
  const scripty = /<script[\s/>]/i.test(source.get());
  $("ed-visual").disabled = scripty && !ed.visual;
  $("ed-visual").title = scripty ? "This page has scripts; edit its source instead." : "";
  $("ed-visual").textContent = ed.visual ? "Preview" : "Edit text visually";
}

// Called on every edit: refresh the preview and save the draft once typing stops.
// A visual edit already shows on screen, and reloading the frame would drop the caret, so it skips the refresh.
function changed(fromVisual) {
  ed.saved = false;
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => { if (!fromVisual) showPreview(); updateButtons(); saveDraft(); }, 600);
}

function saveDraft() {
  clearTimeout(saveTimer);
  if (ed.saved) return saving;
  if (!ed.name) {
    const n = $("ed-name");
    if (!n.value || !n.checkValidity()) {
      setState("Pick a page name (a-z, 0-9 and -) to save the draft.", true);
      return saving;
    }
    if (existing.has(n.value)) {
      setState(`You already have a page called ${n.value}; pick another name or edit that page.`, true);
      return saving;
    }
    ed.name = n.value;
    $("ed-name-field").hidden = true;
    $("ed-title").textContent = `New page: ${ed.name}`;
  }
  const body = { html: source.get() };
  if (ed.base) body.base = ed.base;
  saving = saving.then(async () => {
    try {
      await api("PUT", `/api/drafts/${ed.name}`, JSON.stringify(body));
      ed.saved = source.get() === body.html;
      drafts.add(ed.name);
      setState(`Draft saved ${new Date().toLocaleTimeString()}. Not published yet.`);
    } catch (e) {
      setState(`Draft not saved: ${e.message}`, true);
    }
    updateButtons();
  });
  return saving;
}

// The preview runs in /preview, an opaque-origin sandbox; it asks for the HTML once it has loaded.
function showPreview() {
  $("ed-preview").src = `/preview?${ed.visual ? "mode=edit&" : ""}${Date.now()}`;
}

addEventListener("message", (e) => {
  if (!ed || e.source !== $("ed-preview").contentWindow) return;
  if (typeof e.data?.visual === "string") {
    source.set(e.data.visual);
    return changed(true);
  }
  if (e.data !== "preview-ready") return;
  let html = source.get();
  if (!ed.isNew) {
    // relative links (images, css) of an existing page load from its live address; only the preview copy gets this
    const base = `<base data-ed href="https://${ed.name}.${domain}/">`;
    const head = html.match(/<head[^>]*>/i);
    html = head ? html.replace(head[0], head[0] + base) : base + html;
  }
  e.source.postMessage(html, "*");
});

$("ed-name").addEventListener("change", () => saveDraft());
$("ed-visual").addEventListener("click", () => {
  ed.visual = !ed.visual;
  updateButtons();
  showPreview();
});

$("ed-publish").addEventListener("click", async () => {
  await saveDraft();
  if (!ed.saved) return;
  const url = `https://${ed.name}.${domain}/`;
  try {
    await api("POST", `/api/drafts/${ed.name}/publish`);
  } catch (e) {
    if (e.status !== 412 || !confirm(`${e.message}\n\nPublish your version anyway?`)) return setState(e.message, true);
    try {
      await api("POST", `/api/drafts/${ed.name}/publish?force=1`);
    } catch (e2) {
      return setState(e2.message, true);
    }
  }
  drafts.delete(ed.name);
  $("editor").close();
  $("status").replaceChildren("Published ", Object.assign(document.createElement("a"), { href: url, textContent: url, target: "_blank", rel: "noopener" }));
  $("status").className = "ok";
  refresh();
});

$("ed-discard").addEventListener("click", async () => {
  if (!confirm("Discard this draft? The live page stays as it is.")) return;
  clearTimeout(saveTimer);
  ed.saved = true;
  await api("DELETE", `/api/drafts/${ed.name}`).catch((e) => e.status === 404 || showError(e));
  drafts.delete(ed.name);
  if (existing.has(ed.name)) return openEditor(ed.name, false);
  $("editor").close();
});

$("ed-close").addEventListener("click", async () => {
  await saveDraft();
  if (!ed.saved && !confirm("This draft is not saved. Close anyway?")) return;
  $("editor").close();
});
$("editor").addEventListener("cancel", (e) => { e.preventDefault(); $("ed-close").click(); }); // Esc
$("editor").addEventListener("close", () => refresh());

$("new-page").addEventListener("click", () => openEditor("", true));

// Favicon: the server swaps the <link rel="icon"> tags without touching the rest of the HTML.
async function setFavicon(href) {
  try {
    const { html } = await api("POST", "/api/favicon", JSON.stringify({ html: source.get(), href }));
    source.set(html);
    changed();
  } catch (e) {
    setState(e.message, true);
  }
}

$("ed-favicon").addEventListener("click", () => $("ed-favicon-file").click());
$("ed-favicon-file").addEventListener("change", (e) => {
  const f = e.target.files[0];
  e.target.value = "";
  if (!f) return;
  if (f.size > 48 * 1024) return setState("Pick an image under 48 KB; a favicon is shown at 16 to 64 pixels.", true);
  const r = new FileReader();
  r.onload = () => setFavicon(r.result);
  r.readAsDataURL(f);
});
$("ed-emoji").addEventListener("change", (e) => {
  const emoji = e.target.value.trim().replace(/[<&"]/g, "");
  if (!emoji) return;
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><text y=".9em" font-size="90">${emoji}</text></svg>`;
  setFavicon(`data:image/svg+xml,${encodeURIComponent(svg)}`);
  e.target.value = "";
});
