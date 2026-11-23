"use strict";

const $ = (id) => document.getElementById(id);
let chosen = []; // [{path, file}]
let domain = "";

async function api(method, url, body) {
  const res = await fetch(url, { method, body });
  if (!res.ok) throw Object.assign(new Error((await res.text()).trim()), { status: res.status });
  return res.status === 204 ? null : res.json();
}

async function start() {
  try {
    const me = await api("GET", "/api/me");
    $("who").textContent = me.id;
    $("logout").hidden = false;
    $("app").hidden = false;
    await refresh();
  } catch (e) {
    if (e.status === 403) {
      $("denied").textContent = e.message;
      $("denied").hidden = false;
      $("logout").hidden = false;
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

async function refresh() {
  const data = await api("GET", "/api/pages");
  domain = data.domain;
  const rows = data.pages.map((p) => {
    const tr = document.createElement("tr");
    const url = `https://${p.name}.${domain}/`;
    const link = Object.assign(document.createElement("a"), { href: url, textContent: url, target: "_blank", rel: "noopener" });
    const del = Object.assign(document.createElement("button"), { textContent: "Delete" });
    del.onclick = async () => {
      if (!confirm(`Delete ${p.name}? This cannot be undone.`)) return;
      await api("DELETE", `/api/pages/${p.name}`).catch(showError);
      refresh();
    };
    for (const c of [link, `${(p.bytes / 1024).toFixed(1)} KB`, new Date(p.updated).toLocaleString(), del]) {
      const td = document.createElement("td");
      td.append(c);
      tr.append(td);
    }
    return tr;
  });
  $("pages").replaceChildren(...rows);
  updatePreview();
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
  $("preview").textContent = n ? `→ https://${n}.${domain}/` : "";
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
    $("status").textContent = `Published https://${m.name}.${domain}/`;
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

$("logout").addEventListener("click", async () => {
  await fetch("/logout", { method: "POST" });
  location.reload();
});

start();
