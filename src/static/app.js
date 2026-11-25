"use strict";

const $ = (id) => document.getElementById(id);
let chosen = []; // [{path, file}]
let domain = "";
let existing = new Set(); // page names that publishing would replace

async function api(method, url, body) {
  const res = await fetch(url, { method, body });
  if (!res.ok) throw Object.assign(new Error((await res.text()).trim()), { status: res.status });
  return res.status === 204 ? null : res.json();
}

async function start() {
  try {
    const me = await api("GET", "/api/me");
    const [provider] = me.id.split(":");
    $("who").textContent = me.name ? `${me.name} · ${provider[0].toUpperCase()}${provider.slice(1)}` : me.id;
    $("who").title = me.id; // the stable id the allowlist uses
    $("logout").hidden = false;
    $("app").hidden = false;
    await refresh();
    await refreshTokens();
  } catch (e) {
    if (e.status === 403) {
      $("denied").textContent = e.message;
      $("denied").hidden = false;
      $("logout").hidden = false;
      return;
    }
    // Only a 401 means "not logged in"; any other error (a bug, the network) must not bring the login box back.
    if (e.status !== 401) {
      $("denied").textContent = `Could not load the admin: ${e.message}`;
      $("denied").hidden = false;
      return;
    }
    const providers = await api("GET", "/api/providers");
    for (const p of providers) {
      const a = document.createElement("a");
      a.href = `/auth/${p}/start`;
      a.textContent = `Log in with ${p[0].toUpperCase()}${p.slice(1)}`;
      $("providers").append(a);
    }
    $("login").hidden = false;
  }
}

// Suggested starter tags; any tag already used on a page is suggested too.
const STARTER_TAGS = ["programming", "go", "learning", "interview-prep", "security", "dashboards", "travel", "finance", "productivity"];
let pages = [];
let sortBy = { key: "name", dir: 1 };
let filter = new Set(); // selected tag chips; a page must have all of them

async function refresh() {
  const data = await api("GET", "/api/pages");
  domain = data.domain;
  pages = data.pages;
  existing = new Set(pages.map((p) => p.name));
  render();
  updatePreview();
}

function render() {
  const { key, dir } = sortBy;
  const val = (p) => (key === "bytes" ? p.bytes : key === "updated" ? Date.parse(p.updated) : p.name);
  const sorted = [...pages].sort((a, b) => (val(a) < val(b) ? -1 : val(a) > val(b) ? 1 : 0) * dir);
  for (const th of document.querySelectorAll("th[data-key]")) {
    th.setAttribute("aria-sort", th.dataset.key === key ? (dir > 0 ? "ascending" : "descending") : "none");
  }
  const used = [...new Set(pages.flatMap((p) => p.tags || []))].sort();
  const known = new Set([...STARTER_TAGS, ...used]);
  $("tag-options").replaceChildren(...[...known].sort().map((t) => Object.assign(document.createElement("option"), { value: t })));

  filter = new Set(used.filter((t) => filter.has(t))); // forget chips whose tag no longer exists
  const chips = used.map((t) => {
    const b = Object.assign(document.createElement("button"), { type: "button", className: "chip", textContent: t });
    b.setAttribute("aria-pressed", String(filter.has(t)));
    b.onclick = () => {
      filter.has(t) ? filter.delete(t) : filter.add(t);
      render();
    };
    return b;
  });
  if (filter.size) {
    const clear = Object.assign(document.createElement("button"), { type: "button", className: "chip clear", textContent: "Show all" });
    clear.onclick = () => {
      filter.clear();
      render();
    };
    chips.push(clear);
  }
  $("tag-chips").replaceChildren(...chips);
  $("tag-filter").hidden = used.length === 0;

  const shown = sorted.filter((p) => [...filter].every((t) => (p.tags || []).includes(t)));
  const rows = shown.map((p) => {
    const tr = document.createElement("tr");
    const url = `https://${p.name}.${domain}/`;
    const link = Object.assign(document.createElement("a"), { href: url, textContent: url, target: "_blank", rel: "noopener" });
    // shortcut: the datalist completes only the first tag of the comma list; a chip picker if this gets used a lot
    const tags = Object.assign(document.createElement("input"), {
      value: (p.tags || []).join(", "), className: "tags", placeholder: "add tags", autocomplete: "off", spellcheck: false,
    });
    tags.setAttribute("list", "tag-options");
    tags.setAttribute("aria-label", `Tags for ${p.name}, comma-separated`);
    tags.onchange = async () => {
      try {
        const m = await api("PUT", `/api/pages/${p.name}/tags`, JSON.stringify(tags.value.split(",")));
        p.tags = m.tags;
        render();
      } catch (e) {
        showError(e);
      }
    };
    const del = Object.assign(document.createElement("button"), { textContent: "Delete", className: "del" });
    del.onclick = async () => {
      if (!confirm(`Delete ${p.name}? This cannot be undone.`)) return;
      await api("DELETE", `/api/pages/${p.name}`).catch(showError);
      refresh();
    };
    for (const c of [link, tags, size(p.bytes), new Date(p.updated).toLocaleString(), del]) {
      const td = document.createElement("td");
      td.append(c);
      tr.append(td);
    }
    return tr;
  });
  if (rows.length === 0) {
    const text = pages.length ? "No pages have all the selected tags." : "No pages yet. Drop a site above to publish it.";
    const td = Object.assign(document.createElement("td"), { colSpan: 5, className: "empty", textContent: text });
    const tr = document.createElement("tr");
    tr.append(td);
    rows.push(tr);
  }
  $("pages").replaceChildren(...rows);
}

for (const th of document.querySelectorAll("th[data-key]")) {
  th.querySelector("button").addEventListener("click", () => {
    const key = th.dataset.key;
    // newest and biggest first feel natural; names start A to Z
    sortBy = { key, dir: sortBy.key === key ? -sortBy.dir : key === "name" ? 1 : -1 };
    render();
  });
}

async function refreshTokens() {
  const list = await api("GET", "/api/tokens");
  const items = list.map((t) => {
    const li = document.createElement("li");
    const del = Object.assign(document.createElement("button"), { textContent: "Revoke", className: "del" });
    del.onclick = async () => {
      if (!confirm(`Revoke "${t.label}"? Anything using it stops working.`)) return;
      await api("DELETE", `/api/tokens/${t.id}`).catch(showError);
      refreshTokens();
    };
    li.append(Object.assign(document.createElement("span"), { textContent: `${t.label} · ${new Date(t.created).toLocaleDateString()}` }), del);
    return li;
  });
  $("token-list").replaceChildren(...items);
}

function size(b) {
  return b < 1024 * 1024 ? `${(b / 1024).toFixed(1)} KB` : `${(b / 1024 / 1024).toFixed(1)} MB`;
}

function slug(s) {
  return s.toLowerCase().replace(/\.(zip|html?)$/, "").replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63);
}

function choose(files, label) {
  chosen = files;
  $("chosen").textContent = `${label} (${files.length} file${files.length === 1 ? "" : "s"})`;
  if (!$("name").value) $("name").value = slug(label);
  $("publish").disabled = files.length === 0;
  updatePreview();
}

function updatePreview() {
  const n = $("name").value;
  $("preview").textContent = n ? `→ https://${n}.${domain}/${existing.has(n) ? " (replaces the page you have now)" : ""}` : "";
}

// Reads a dropped folder recursively; paths keep the folder name, the server strips it.
async function walk(entry, prefix, out) {
  if (entry.isFile) {
    const file = await new Promise((ok, fail) => entry.file(ok, fail));
    out.push({ path: prefix + entry.name, file });
    return;
  }
  const reader = entry.createReader();
  for (;;) {
    const batch = await new Promise((ok, fail) => reader.readEntries(ok, fail));
    if (batch.length === 0) return;
    for (const e of batch) await walk(e, `${prefix}${entry.name}/`, out);
  }
}

function showError(e) {
  $("status").textContent = e.message;
  $("status").className = "error";
}

$("drop").addEventListener("dragover", (e) => { e.preventDefault(); $("drop").classList.add("over"); });
$("drop").addEventListener("dragleave", () => $("drop").classList.remove("over"));
$("drop").addEventListener("drop", async (e) => {
  e.preventDefault();
  $("drop").classList.remove("over");
  const entries = [...e.dataTransfer.items].map((i) => i.webkitGetAsEntry()).filter(Boolean);
  if (entries.length !== 1) return showError(new Error("Drop one folder, zip or html file."));
  const out = [];
  await walk(entries[0], "", out);
  choose(out, entries[0].name);
});
$("pick").addEventListener("change", (e) => {
  const f = e.target.files[0];
  if (f) choose([{ path: f.name, file: f }], f.name);
});
$("pick-dir").addEventListener("click", () => $("pick-dir-input").click());
$("pick-dir-input").addEventListener("change", (e) => {
  const files = [...e.target.files].map((f) => ({ path: f.webkitRelativePath, file: f }));
  if (files.length) choose(files, files[0].path.split("/")[0]);
});
$("name").addEventListener("input", updatePreview);

$("upload").addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData();
  fd.append("name", $("name").value);
  for (const { path, file } of chosen) {
    fd.append("path", path);
    fd.append("file", file);
  }
  $("publish").disabled = true;
  $("status").className = "";
  $("status").textContent = "Publishing…";
  try {
    const m = await api("POST", "/api/pages", fd);
    const url = `https://${m.name}.${domain}/`;
    $("status").replaceChildren("Published ", Object.assign(document.createElement("a"), { href: url, textContent: url, target: "_blank", rel: "noopener" }));
    $("status").className = "ok";
    $("name").value = "";
    chosen = [];
    $("chosen").textContent = "";
    await refresh();
  } catch (err) {
    showError(err);
  } finally {
    $("publish").disabled = chosen.length === 0;
  }
});

$("token-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  try {
    const t = await api("POST", "/api/tokens", JSON.stringify({ label: $("token-label").value }));
    $("token-value").textContent = t.token;
    $("token-new").hidden = false;
    $("token-label").value = "";
    await refreshTokens();
  } catch (err) {
    showError(err);
  }
});

$("logout").addEventListener("click", async () => {
  await fetch("/logout", { method: "POST" });
  location.reload();
});

start();
