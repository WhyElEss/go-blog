// Admin panel: Markdown editor with live preview, image uploads (button,
// paste, drop), cover and logo uploads, confirmations.
(function () {
  "use strict";

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

  // Forms with data-confirm ask first.
  $$("form[data-confirm]").forEach((f) =>
    f.addEventListener("submit", (e) => {
      if (!confirm(f.dataset.confirm)) e.preventDefault();
    })
  );

  // Copy buttons.
  $$("[data-copy]").forEach((b) =>
    b.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(b.dataset.copy);
        const old = b.textContent;
        b.textContent = b.dataset.copied;
        setTimeout(() => (b.textContent = old), 1200);
      } catch (_) {
        prompt("", b.dataset.copy);
      }
    })
  );

  const status = $("#upload-status");
  const setStatus = (text, bad) => {
    if (!status) return;
    status.textContent = text;
    status.classList.toggle("bad", !!bad);
  };

  // upload sends one file and resolves to {url, width, height}.
  async function upload(file) {
    setStatus(status ? status.dataset.uploading + " " + file.name : "");
    const fd = new FormData();
    fd.append("file", file);
    let res, data;
    try {
      res = await fetch("/admin/upload", { method: "POST", body: fd, credentials: "same-origin" });
      data = await res.json();
    } catch (err) {
      setStatus((status ? status.dataset.failed : "") + err, true);
      throw err;
    }
    if (!res.ok) {
      setStatus((status ? status.dataset.failed : "") + (data.error || res.status), true);
      throw new Error(data.error || res.status);
    }
    setStatus("");
    return data;
  }

  // pickFile opens the file dialog and resolves to the chosen file.
  const fileInput = $("#img-file");
  function pickFile() {
    return new Promise((resolve) => {
      fileInput.value = "";
      fileInput.onchange = () => fileInput.files[0] && resolve(fileInput.files[0]);
      fileInput.click();
    });
  }

  // Upload buttons that fill a text field (logo, icon, cover).
  $$("[data-field-upload]").forEach((b) =>
    b.addEventListener("click", async () => {
      const file = await pickFile();
      const { url } = await upload(file);
      const input = $("#" + b.dataset.fieldUpload);
      input.value = url;
      input.dispatchEvent(new Event("input", { bubbles: true }));
    })
  );

  // Image library page.
  const lib = $("[data-library-upload]");
  if (lib) {
    lib.addEventListener("click", async () => {
      const file = await pickFile();
      await upload(file);
      location.reload();
    });
  }

  const form = $("#editor");
  if (!form) return;

  const body = $("#body");
  const preview = $("#preview");

  // Unsaved changes warning.
  let dirty = false;
  form.addEventListener("input", () => (dirty = true));
  form.addEventListener("submit", () => (dirty = false));
  window.addEventListener("beforeunload", (e) => {
    if (dirty) {
      e.preventDefault();
      e.returnValue = form.dataset.unsaved;
    }
  });

  // Live preview, rendered by the server so it matches the site exactly.
  let timer = null;
  let seq = 0;
  async function refresh() {
    const my = ++seq;
    const fd = new FormData();
    fd.append("body", body.value);
    try {
      const res = await fetch("/admin/preview", { method: "POST", body: fd, credentials: "same-origin" });
      if (res.ok && my === seq) preview.innerHTML = await res.text();
    } catch (_) {
      /* keep the old preview */
    }
  }
  body.addEventListener("input", () => {
    clearTimeout(timer);
    timer = setTimeout(refresh, 300);
  });
  refresh();

  // insert replaces the selection with text and selects the part between
  // the markers, if any.
  function insert(before, after = "", placeholder = "") {
    const s = body.selectionStart, e = body.selectionEnd;
    const sel = body.value.slice(s, e) || placeholder;
    body.focus();
    body.setRangeText(before + sel + after, s, e, "end");
    body.setSelectionRange(s + before.length, s + before.length + sel.length);
    body.dispatchEvent(new Event("input", { bubbles: true }));
  }

  // insertBlock puts text on its own lines, as images and embeds need.
  function insertBlock(text) {
    const s = body.selectionStart;
    const v = body.value;
    let pre = "";
    if (s > 0 && v[s - 1] !== "\n") pre = "\n\n";
    else if (s > 1 && v[s - 2] !== "\n") pre = "\n";
    let post = "\n\n";
    if (v[body.selectionEnd] === "\n") post = "\n";
    body.focus();
    body.setRangeText(pre + text + post, s, body.selectionEnd, "end");
    body.dispatchEvent(new Event("input", { bubbles: true }));
  }

  // linePrefix adds a prefix to every selected line.
  function linePrefix(prefix) {
    const v = body.value;
    let s = body.selectionStart;
    while (s > 0 && v[s - 1] !== "\n") s--;
    const e = body.selectionEnd;
    const lines = v.slice(s, e).split("\n").map((l) => prefix + l.replace(/^(#{1,6} |> |- )/, ""));
    body.focus();
    body.setRangeText(lines.join("\n"), s, e, "end");
    body.dispatchEvent(new Event("input", { bubbles: true }));
  }

  const actions = {
    bold: () => insert("**", "**", "text"),
    italic: () => insert("_", "_", "text"),
    h2: () => linePrefix("## "),
    h3: () => linePrefix("### "),
    quote: () => linePrefix("> "),
    list: () => linePrefix("- "),
    link: () => {
      const url = prompt("URL", "https://");
      if (url) insert("[", "](" + url + ")", "link");
    },
  };
  $$("[data-md]").forEach((b) => b.addEventListener("click", () => actions[b.dataset.md]()));

  const imageMarkdown = (url, align) => "![](" + url + (align && align !== "center" ? "#" + align : "") + ")";

  async function uploadInto(file, align) {
    const { url } = await upload(file);
    insertBlock(imageMarkdown(url, align));
  }

  $$("[data-img]").forEach((b) =>
    b.addEventListener("click", async () => {
      // Remember the caret: the file dialog takes the focus away.
      const s = body.selectionStart, e = body.selectionEnd;
      const file = await pickFile();
      body.setSelectionRange(s, e);
      uploadInto(file, b.dataset.img);
    })
  );

  body.addEventListener("paste", (e) => {
    const files = Array.from(e.clipboardData.files || []).filter((f) => f.type.startsWith("image/"));
    if (!files.length) return;
    e.preventDefault();
    files.reduce((p, f) => p.then(() => uploadInto(f, "center")), Promise.resolve());
  });
  body.addEventListener("dragover", (e) => {
    if (Array.from(e.dataTransfer.items || []).some((i) => i.kind === "file")) {
      e.preventDefault();
      body.classList.add("drop");
    }
  });
  body.addEventListener("dragleave", () => body.classList.remove("drop"));
  body.addEventListener("drop", (e) => {
    const files = Array.from(e.dataTransfer.files || []).filter((f) => f.type.startsWith("image/"));
    body.classList.remove("drop");
    if (!files.length) return;
    e.preventDefault();
    files.reduce((p, f) => p.then(() => uploadInto(f, "center")), Promise.resolve());
  });

  // Cover.
  const cover = $("#cover");
  const coverPreview = $("#cover-preview");
  const showCover = () => {
    coverPreview.hidden = !cover.value;
    if (cover.value) coverPreview.src = cover.value;
  };
  cover.addEventListener("input", showCover);
  $("[data-cover-upload]").addEventListener("click", async () => {
    const file = await pickFile();
    const { url } = await upload(file);
    cover.value = url;
    dirty = true;
    showCover();
  });
  $("[data-cover-clear]").addEventListener("click", () => {
    cover.value = "";
    dirty = true;
    showCover();
  });

  // Shortcuts: ⌘S saves, ⌘B / ⌘I / ⌘K format.
  document.addEventListener("keydown", (e) => {
    if (!(e.metaKey || e.ctrlKey)) return;
    const k = e.key.toLowerCase();
    if (k === "s") {
      e.preventDefault();
      form.requestSubmit(form.querySelector('button[value="save"]'));
    } else if (document.activeElement === body && actions[{ b: "bold", i: "italic", k: "link" }[k]]) {
      e.preventDefault();
      actions[{ b: "bold", i: "italic", k: "link" }[k]]();
    }
  });
})();
