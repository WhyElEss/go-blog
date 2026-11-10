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

  // upload sends one file and resolves to {url, width, height}. name, if
  // given, names the stored file instead of the file's own name; numbered
  // makes the server add the next number (name-01, name-02…).
  async function upload(file, name, numbered) {
    setStatus(status ? status.dataset.uploading + " " + file.name : "");
    const fd = new FormData();
    fd.append("file", file);
    if (name) fd.append("name", name);
    if (name && numbered) fd.append("numbered", "1");
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

  // One title, text and excerpt per language; the tabs show one set, and
  // the toolbar, uploads and preview work on the text in view.
  const bodies = $$("textarea.md");
  const preview = $("#preview");
  let body = bodies[0];
  const tabs = $$("[data-tab]");
  const markEmpty = () =>
    tabs.forEach((tab) => {
      const l = tab.dataset.tab;
      const filled = $$('[data-pane="' + l + '"] textarea, input[data-pane="' + l + '"], textarea[data-pane="' + l + '"]').some((el) => el.value.trim());
      tab.classList.toggle("empty", !filled);
    });
  function showTab(lang) {
    tabs.forEach((tab) => {
      const on = tab.dataset.tab === lang;
      tab.classList.toggle("on", on);
      tab.setAttribute("aria-selected", on);
    });
    $$("[data-pane]").forEach((el) => (el.hidden = el.dataset.pane !== lang));
    body = bodies.find((b) => b.dataset.pane === lang);
    preview.lang = lang;
    refresh();
  }
  tabs.forEach((tab) => tab.addEventListener("click", () => showTab(tab.dataset.tab)));
  form.addEventListener("input", markEmpty);
  markEmpty();

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
  bodies.forEach((b) =>
    b.addEventListener("input", () => {
      clearTimeout(timer);
      timer = setTimeout(refresh, 300);
    })
  );
  preview.lang = body.lang;
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

  // linePrefix puts a prefix on every selected line, in place of any
  // heading, quote or list marker it had. prefix may be a function of the
  // line's number, for numbered lists.
  function linePrefix(prefix) {
    const v = body.value;
    let s = body.selectionStart;
    while (s > 0 && v[s - 1] !== "\n") s--;
    const e = body.selectionEnd;
    const at = typeof prefix === "function" ? prefix : () => prefix;
    const lines = v
      .slice(s, e)
      .split("\n")
      .map((l, i) => at(i) + l.replace(/^(#{1,6} |> |[-*+] \[[ xX]\] |[-*+] |\d+[.)] )/, ""));
    body.focus();
    body.setRangeText(lines.join("\n"), s, e, "end");
    body.dispatchEvent(new Event("input", { bubbles: true }));
  }

  // fenced wraps the selection, or a placeholder, in a code block and
  // selects what is inside.
  function fenced() {
    const s = body.selectionStart, e = body.selectionEnd;
    const sel = body.value.slice(s, e) || "code";
    insertBlock("```\n" + sel + "\n```");
    const at = body.value.lastIndexOf(sel + "\n```", body.selectionEnd);
    if (at >= 0) body.setSelectionRange(at, at + sel.length);
  }

  // footnote puts the next free [^n] at the caret and its note at the end
  // of the text, with the caret there to write it.
  function footnote() {
    const used = Array.from(body.value.matchAll(/\[\^(\d+)\]/g), (m) => +m[1]);
    const n = used.length ? Math.max(...used) + 1 : 1;
    const e = body.selectionEnd;
    body.focus();
    body.setRangeText("[^" + n + "]", e, e, "end");
    const v = body.value.replace(/\s*$/, "");
    body.value = v + "\n\n[^" + n + "]: ";
    body.setSelectionRange(body.value.length, body.value.length);
    body.dispatchEvent(new Event("input", { bubbles: true }));
  }

  const toolbar = $(".toolbar");
  const actions = {
    bold: () => insert("**", "**", "text"),
    italic: () => insert("_", "_", "text"),
    strike: () => insert("~~", "~~", "text"),
    mark: () => insert("==", "==", "text"),
    code: () => insert("`", "`", "code"),
    h2: () => linePrefix("## "),
    h3: () => linePrefix("### "),
    h4: () => linePrefix("#### "),
    quote: () => linePrefix("> "),
    list: () => linePrefix("- "),
    olist: () => linePrefix((i) => i + 1 + ". "),
    task: () => linePrefix("- [ ] "),
    codeblock: fenced,
    table: () => insertBlock("| Column | Column |\n| --- | --- |\n| Cell | Cell |\n| Cell | Cell |"),
    hr: () => insertBlock("---"),
    footnote: footnote,
    toc: () => insertBlock("[TOC]"),
    link: () => {
      const url = prompt(toolbar.dataset.linkAsk, "https://");
      if (url) insert("[", "](" + url + ")", "link");
    },
    youtube: () => {
      const url = prompt(toolbar.dataset.videoAsk, "https://www.youtube.com/watch?v=");
      if (url && /^https?:\/\/\S+$/.test(url.trim())) insertBlock(url.trim());
    },
    music: () => {
      const url = prompt(toolbar.dataset.musicAsk, "https://music.apple.com/");
      if (url && /^https:\/\/music\.apple\.com\/\S+$/.test(url.trim())) insertBlock(url.trim());
    },
  };
  $$("[data-md]").forEach((b) => b.addEventListener("click", () => actions[b.dataset.md]()));

  const imageMarkdown = (url, align) => "![](" + url + (align && align !== "center" ? "#" + align : "") + ")";

  // Pictures in the text are named after the post's URL, numbered
  // ("halloween-candies-01"), or after its title while the URL is empty.
  const postName = () => {
    const slug = $('input[name="slug"]').value.trim();
    const title = $('input[name="title_' + $('select[name="lang"]').value + '"]');
    return slug || (title ? title.value.trim() : "");
  };

  async function uploadInto(file, align) {
    const { url } = await upload(file, postName(), true);
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

  // Gallery: several files at once, uploaded one after another, then
  // [gallery] with an image per line (markdown.go). A failed upload stops
  // the rest, so its error stays in view; the ones uploaded go in.
  const galleryInput = $("#gallery-files");
  const galleryButton = $("[data-gallery]");
  if (galleryButton && galleryInput) {
    galleryButton.addEventListener("click", () => {
      const s = body.selectionStart, e = body.selectionEnd;
      galleryInput.value = "";
      galleryInput.onchange = async () => {
        const files = Array.from(galleryInput.files || []);
        if (!files.length) return;
        const urls = [];
        for (const f of files) {
          try {
            urls.push((await upload(f, postName(), true)).url);
          } catch {
            break;
          }
        }
        if (!urls.length) return;
        body.setSelectionRange(s, e);
        insertBlock("[gallery]\n" + urls.map((u) => "![](" + u + ")").join("\n"));
      };
      galleryInput.click();
    });
  }

  bodies.forEach((b) => {
    b.addEventListener("paste", (e) => {
      const files = Array.from(e.clipboardData.files || []).filter((f) => f.type.startsWith("image/"));
      if (!files.length) return;
      e.preventDefault();
      files.reduce((p, f) => p.then(() => uploadInto(f, "center")), Promise.resolve());
    });
    b.addEventListener("dragover", (e) => {
      if (Array.from(e.dataTransfer.items || []).some((i) => i.kind === "file")) {
        e.preventDefault();
        b.classList.add("drop");
      }
    });
    b.addEventListener("dragleave", () => b.classList.remove("drop"));
    b.addEventListener("drop", (e) => {
      const files = Array.from(e.dataTransfer.files || []).filter((f) => f.type.startsWith("image/"));
      b.classList.remove("drop");
      if (!files.length) return;
      e.preventDefault();
      files.reduce((p, f) => p.then(() => uploadInto(f, "center")), Promise.resolve());
    });
  });

  // Tags: a list of the tags already in use under the field. It shows the
  // ones matching the tag being typed, leaves out those already added, and
  // a click or Enter puts the tag in place of what was typed.
  const tagInput = $('input[name="tags"]');
  const tagMenu = $("#tag-menu");
  if (tagInput && tagMenu) {
    const items = $$("li", tagMenu);
    let active = -1;
    const shown = () => items.filter((li) => !li.hidden);
    const setActive = (i) => {
      const list = shown();
      active = list.length ? (i + list.length) % list.length : -1;
      items.forEach((li) => li.classList.remove("on"));
      if (active >= 0) {
        list[active].classList.add("on");
        list[active].scrollIntoView({ block: "nearest" });
      }
    };
    const close = () => {
      tagMenu.hidden = true;
      tagInput.setAttribute("aria-expanded", "false");
      active = -1;
    };
    const open = () => {
      const parts = tagInput.value.split(",");
      const typed = parts[parts.length - 1].trim().toLowerCase();
      const have = new Set(parts.slice(0, -1).map((p) => p.trim().toLowerCase()));
      items.forEach((li) => {
        const name = li.dataset.tag.toLowerCase();
        li.hidden = have.has(name) || (typed !== "" && !name.includes(typed)) || name === typed;
      });
      const any = shown().length > 0;
      tagMenu.hidden = !any;
      tagInput.setAttribute("aria-expanded", String(any));
      active = -1;
      items.forEach((li) => li.classList.remove("on"));
    };
    const pick = (li) => {
      const parts = tagInput.value.split(",");
      parts[parts.length - 1] = " " + li.dataset.tag;
      tagInput.value = parts.map((p) => p.trim()).filter(Boolean).join(", ") + ", ";
      tagInput.dispatchEvent(new Event("input", { bubbles: true }));
      tagInput.focus();
    };
    tagInput.addEventListener("focus", open);
    tagInput.addEventListener("click", open);
    tagInput.addEventListener("input", open);
    tagInput.addEventListener("blur", close);
    // mousedown, not click: a click would blur the field first.
    tagMenu.addEventListener("mousedown", (e) => {
      const li = e.target.closest("li");
      if (!li) return;
      e.preventDefault();
      pick(li);
    });
    tagInput.addEventListener("keydown", (e) => {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        if (tagMenu.hidden) open();
        e.preventDefault();
        setActive(e.key === "ArrowDown" ? active + 1 : active < 0 ? -1 : active - 1);
      } else if (e.key === "Enter" && !tagMenu.hidden && active >= 0) {
        e.preventDefault();
        pick(shown()[active]);
      } else if (e.key === "Escape" && !tagMenu.hidden) {
        e.preventDefault();
        close();
      }
    });
  }

  // Cover, with the frame of its link card copy (1200:630, cards.go):
  // dragged along the side that is cut, saved as cover_pos 0-100.
  const cover = $("#cover");
  const coverPreview = $("#cover-preview");
  const coverCrop = $("#cover-crop");
  const frame = $("#cover-frame");
  const framePos = $("#cover-pos");
  const coverHint = $("#cover-hint");
  const cardRatio = 1200 / 630;
  let frameWide = false; // the cover is wider than a card: the frame moves sideways
  let frameShare = 1; // the frame's share of the side it moves along
  const placeFrame = () => {
    const w = coverPreview.naturalWidth, h = coverPreview.naturalHeight;
    const ok = !coverCrop.hidden && w > 0 && h > 0 && /^\/img\/.+\.(jpg|png)$/.test(cover.value);
    frame.hidden = coverHint.hidden = !ok;
    if (!ok) return;
    const pos = Number(framePos.value);
    frameWide = w / h > cardRatio;
    frameShare = frameWide ? Math.min(1, (h * cardRatio) / w) : Math.min(1, w / cardRatio / h);
    const size = frameShare * 100 + "%", offset = (1 - frameShare) * pos + "%";
    frame.style.width = frameWide ? size : "100%";
    frame.style.height = frameWide ? "100%" : size;
    frame.style.left = frameWide ? offset : "0";
    frame.style.top = frameWide ? "0" : offset;
    frame.setAttribute("aria-valuenow", pos);
  };
  const setFramePos = (pos) => {
    pos = Math.round(Math.min(100, Math.max(0, pos)));
    if (String(pos) === framePos.value) return;
    framePos.value = pos;
    dirty = true;
    placeFrame();
  };
  coverPreview.addEventListener("load", placeFrame);
  frame.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    frame.setPointerCapture(e.pointerId);
    frame.classList.add("dragging");
    const start = frameWide ? e.clientX : e.clientY;
    const startPos = Number(framePos.value);
    const box = coverCrop.getBoundingClientRect();
    const room = (frameWide ? box.width : box.height) * (1 - frameShare);
    const move = (ev) => {
      if (room > 0) setFramePos(startPos + (((frameWide ? ev.clientX : ev.clientY) - start) / room) * 100);
    };
    const end = () => {
      frame.classList.remove("dragging");
      frame.removeEventListener("pointermove", move);
      frame.removeEventListener("pointerup", end);
      frame.removeEventListener("pointercancel", end);
    };
    frame.addEventListener("pointermove", move);
    frame.addEventListener("pointerup", end);
    frame.addEventListener("pointercancel", end);
  });
  frame.addEventListener("keydown", (e) => {
    const step = { ArrowLeft: -5, ArrowUp: -5, ArrowRight: 5, ArrowDown: 5, Home: -100, End: 100 }[e.key];
    if (step === undefined) return;
    e.preventDefault();
    setFramePos(Number(framePos.value) + step);
  });
  // A new cover starts with the frame in the centre.
  // src is the new upload's address with its version, so the preview
  // shows it and not a cached picture under the same name.
  const showCover = (src) => {
    framePos.value = 50;
    coverCrop.hidden = !cover.value;
    if (cover.value) coverPreview.src = typeof src === "string" ? src : cover.value;
    placeFrame();
  };
  cover.addEventListener("input", showCover);
  if (coverPreview.complete) placeFrame();
  // The cover is named after the post's URL ("halloween-candies-cover"),
  // or its title when the URL is still empty, as the server does.
  const coverName = () => {
    const base = postName();
    return base ? base + "-cover" : "";
  };
  $("[data-cover-upload]").addEventListener("click", async () => {
    const file = await pickFile();
    const { url, src } = await upload(file, coverName());
    cover.value = url;
    dirty = true;
    showCover(src);
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
