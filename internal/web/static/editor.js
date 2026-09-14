/*
 * Markspace markdown editor. It enhances the plain editor form, which still
 * saves through a normal POST when scripting is off.
 */
(function () {
  "use strict";

  var form = document.querySelector("form[data-editor]");
  if (!form) {
    return;
  }

  var M = window.Markspace || {};
  var el = M.el || function (tag, className, text) {
    var node = document.createElement(tag);
    if (className) {
      node.className = className;
    }
    if (text !== undefined && text !== null) {
      node.textContent = String(text);
    }
    return node;
  };

  var noteId = form.getAttribute("data-note-id");
  var spaceId = form.getAttribute("data-space-id");
  var noteUrl = form.getAttribute("data-note-url");
  var baseUpdatedAt = form.getAttribute("data-updated-at") || "";

  var text = form.querySelector("[data-editor-text]");
  var titleInput = form.querySelector("[data-editor-title]");
  var tagsInput = form.querySelector("[data-editor-tags]");
  var iconInput = form.querySelector("[data-editor-icon]");
  var baseInput = form.querySelector("[data-base-updated]");
  var toolbar = form.querySelector("[data-toolbar]");
  var preview = form.querySelector("[data-preview]");
  var panes = form.querySelector("[data-panes]");
  var status = form.querySelector("[data-save-status]");
  var conflict = form.querySelector("[data-conflict]");
  var uploads = form.querySelector("[data-uploads]");
  var uploadInput = form.querySelector("[data-upload-input]");
  var suggestList = form.querySelector("[data-wikilink-list]");

  var PREVIEW_DELAY = 250;
  var AUTOSAVE_DELAY = 2000;

  var dirty = false;
  var saving = false;
  var inConflict = false;
  var saveTimer = null;
  var previewTimer = null;
  var previewSeq = 0;

  toolbar.hidden = false;
  preview.hidden = false;
  panes.classList.add("with-preview");
  var aiPanel = form.querySelector("[data-ai-panel]");
  if (aiPanel) {
    aiPanel.hidden = false;
  }

  /* ---------- Requests ---------- */

  function request(method, url, payload) {
    var init = {
      method: method,
      credentials: "same-origin",
      headers: { Accept: "application/json" }
    };
    if (payload !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(payload);
    }
    return fetch(url, init).then(function (res) {
      var type = res.headers.get("Content-Type") || "";
      var parse = type.indexOf("json") >= 0 ? res.json() : res.text();
      return parse.then(function (data) {
        if (!res.ok) {
          var err = new Error((data && data.error) || res.statusText || "Request failed");
          err.status = res.status;
          throw err;
        }
        return data;
      });
    });
  }

  function unwrap(data, key) {
    if (data && typeof data === "object" && data[key] && typeof data[key] === "object") {
      return data[key];
    }
    return data;
  }

  /* ---------- Save status and autosave ---------- */

  function setStatus(state, label) {
    status.setAttribute("data-state", state);
    status.textContent = label;
  }

  function markDirty() {
    dirty = true;
    if (!inConflict) {
      setStatus("dirty", "Unsaved changes");
    }
    clearTimeout(saveTimer);
    saveTimer = setTimeout(function () {
      save(false);
    }, AUTOSAVE_DELAY);
  }

  function payload(overwrite) {
    var body = {
      title: titleInput.value,
      content: text.value,
      tags: tagsInput ? splitTags(tagsInput.value) : undefined,
      icon: iconInput ? iconInput.value.trim() : undefined
    };
    if (!overwrite && baseUpdatedAt) {
      body.baseUpdatedAt = baseUpdatedAt;
    }
    return body;
  }

  function splitTags(value) {
    var seen = {};
    return value.split(/[,\n]/).map(function (t) {
      return t.trim().replace(/^#/, "");
    }).filter(function (t) {
      if (!t || seen[t]) {
        return false;
      }
      seen[t] = true;
      return true;
    });
  }

  /* save resolves true when the note was stored. overwrite drops the
   * optimistic lock, which is how the conflict banner forces a save. */
  function save(overwrite) {
    clearTimeout(saveTimer);
    if (saving) {
      saveTimer = setTimeout(function () {
        save(overwrite);
      }, 500);
      return Promise.resolve(false);
    }
    if (!dirty && !overwrite) {
      return Promise.resolve(true);
    }
    if (inConflict && !overwrite) {
      return Promise.resolve(false);
    }
    saving = true;
    setStatus("saving", "Saving");
    var sentContent = text.value;
    var sentTitle = titleInput.value;
    return request("PATCH", "/api/notes/" + encodeURIComponent(noteId), payload(overwrite))
      .then(function (data) {
        var note = unwrap(data, "note");
        if (note && note.updatedAt) {
          baseUpdatedAt = note.updatedAt;
          if (baseInput) {
            baseInput.value = baseUpdatedAt;
          }
        }
        inConflict = false;
        conflict.hidden = true;
        dirty = text.value !== sentContent || titleInput.value !== sentTitle;
        if (dirty) {
          markDirty();
        } else {
          setStatus("saved", "Saved");
        }
        return true;
      })
      .catch(function (err) {
        if (err.status === 409) {
          inConflict = true;
          conflict.hidden = false;
          setStatus("error", "Conflict");
        } else {
          setStatus("error", "Not saved: " + (err.message || "error"));
        }
        return false;
      })
      .then(function (ok) {
        saving = false;
        return ok;
      });
  }

  [text, titleInput, tagsInput, iconInput].forEach(function (input) {
    if (input) {
      input.addEventListener("input", markDirty);
    }
  });

  /* Done saves when something changed, then returns to the note. With
   * scripting off the same button posts the form, which saves and redirects. */
  var doneButton = form.querySelector("[data-editor-done]");
  var leaving = false;

  form.addEventListener("submit", function (event) {
    event.preventDefault();
    if (leaving) {
      return;
    }
    leaving = true;
    if (doneButton) {
      doneButton.disabled = true;
    }
    save(false).then(function (ok) {
      if (ok) {
        dirty = false;
        window.location.href = noteUrl;
        return;
      }
      leaving = false;
      if (doneButton) {
        doneButton.disabled = false;
      }
    });
  });

  conflict.querySelector("[data-conflict-reload]").addEventListener("click", function () {
    dirty = false;
    window.location.reload();
  });

  conflict.querySelector("[data-conflict-overwrite]").addEventListener("click", function () {
    save(true);
  });

  window.addEventListener("beforeunload", function (event) {
    if (dirty) {
      event.preventDefault();
      event.returnValue = "";
      return "";
    }
    return undefined;
  });

  document.addEventListener("markspace:note-updated", function (event) {
    var ev = event.detail || {};
    var me = document.body.getAttribute("data-user-id");
    if (ev.actorId && ev.actorId === me) {
      return;
    }
    if (dirty) {
      inConflict = true;
      conflict.hidden = false;
      setStatus("error", "Conflict");
    }
  });

  /* ---------- Preview ---------- */

  function renderPreview() {
    var seq = ++previewSeq;
    request("POST", "/api/render", { spaceId: spaceId, markdown: text.value })
      .then(function (data) {
        if (seq !== previewSeq) {
          return;
        }
        var rendered = unwrap(data, "rendered");
        /* The render endpoint returns sanitized renderer output. */
        preview.innerHTML = rendered && typeof rendered.html === "string" ? rendered.html : "";
        syncScroll();
      })
      .catch(function (err) {
        if (seq === previewSeq) {
          preview.textContent = "";
          preview.appendChild(el("p", "muted", "Preview unavailable: " + (err.message || "error")));
        }
      });
  }

  function schedulePreview() {
    if (preview.hidden) {
      return;
    }
    clearTimeout(previewTimer);
    previewTimer = setTimeout(renderPreview, PREVIEW_DELAY);
  }

  function syncScroll() {
    var range = text.scrollHeight - text.clientHeight;
    var fraction = range > 0 ? text.scrollTop / range : 0;
    preview.scrollTop = fraction * (preview.scrollHeight - preview.clientHeight);
  }

  text.addEventListener("input", schedulePreview);
  text.addEventListener("scroll", syncScroll);
  renderPreview();

  /* ---------- Selection helpers ---------- */

  function replaceRange(start, end, insert, selectFrom, selectTo) {
    text.focus();
    text.setRangeText(insert, start, end, "end");
    if (selectFrom !== undefined) {
      text.setSelectionRange(selectFrom, selectTo === undefined ? selectFrom : selectTo);
    }
    text.dispatchEvent(new Event("input", { bubbles: true }));
  }

  function wrap(before, after, placeholder) {
    var start = text.selectionStart;
    var end = text.selectionEnd;
    var selected = text.value.slice(start, end) || placeholder;
    var insert = before + selected + after;
    replaceRange(start, end, insert, start + before.length, start + before.length + selected.length);
  }

  function lineStart(pos) {
    return text.value.lastIndexOf("\n", pos - 1) + 1;
  }

  function prefixLines(prefix) {
    var start = lineStart(text.selectionStart);
    var end = text.selectionEnd;
    var block = text.value.slice(start, end);
    var lines = block.split("\n").map(function (line, i) {
      var p = typeof prefix === "function" ? prefix(i) : prefix;
      return line.indexOf(p) === 0 ? line.slice(p.length) : p + line;
    });
    var insert = lines.join("\n");
    replaceRange(start, end, insert, start, start + insert.length);
  }

  function insertBlock(snippet) {
    var start = text.selectionStart;
    var before = text.value.slice(0, start);
    var lead = before === "" || /\n\n$/.test(before) ? "" : (/\n$/.test(before) ? "\n" : "\n\n");
    var insert = lead + snippet + "\n";
    replaceRange(start, text.selectionEnd, insert, start + insert.length);
  }

  function insertAtCursor(snippet) {
    var start = text.selectionStart;
    replaceRange(start, text.selectionEnd, snippet, start + snippet.length);
  }

  var commands = {
    bold: function () { wrap("**", "**", "bold text"); },
    italic: function () { wrap("_", "_", "italic text"); },
    code: function () {
      var selected = text.value.slice(text.selectionStart, text.selectionEnd);
      if (selected.indexOf("\n") >= 0) {
        wrap("```\n", "\n```", "");
      } else {
        wrap("`", "`", "code");
      }
    },
    link: function () {
      var start = text.selectionStart;
      var selected = text.value.slice(start, text.selectionEnd) || "link text";
      var insert = "[" + selected + "](https://)";
      var urlStart = start + selected.length + 3;
      replaceRange(start, text.selectionEnd, insert, urlStart, urlStart + 8);
    },
    heading: function () { prefixLines("## "); },
    quote: function () { prefixLines("> "); },
    list: function () { prefixLines("- "); },
    task: function () { prefixLines("- [ ] "); },
    table: function () {
      insertBlock("| Column | Column |\n| ------ | ------ |\n| Cell   | Cell   |");
    },
    rule: function () { insertBlock("---"); },
    "toggle-preview": function (button) {
      preview.hidden = !preview.hidden;
      panes.classList.toggle("with-preview", !preview.hidden);
      button.setAttribute("aria-pressed", preview.hidden ? "false" : "true");
      if (!preview.hidden) {
        renderPreview();
      }
    }
  };

  toolbar.addEventListener("click", function (event) {
    var button = event.target.closest("[data-cmd]");
    if (!button) {
      return;
    }
    event.preventDefault();
    var command = commands[button.getAttribute("data-cmd")];
    if (command) {
      command(button);
    }
  });

  text.addEventListener("keydown", function (event) {
    if (suggestOpen() && handleSuggestKey(event)) {
      return;
    }
    if (!(event.ctrlKey || event.metaKey) || event.altKey) {
      return;
    }
    var key = event.key.toLowerCase();
    if (key === "b") {
      event.preventDefault();
      commands.bold();
    } else if (key === "i") {
      event.preventDefault();
      commands.italic();
    } else if (key === "k") {
      event.preventDefault();
      commands.link();
    } else if (key === "s") {
      event.preventDefault();
      dirty = true;
      save(false);
    }
  });

  /* ---------- Wikilink autocomplete ---------- */

  var suggestItems = [];
  var suggestIndex = -1;
  var suggestSeq = 0;
  var suggestStart = -1;

  function suggestOpen() {
    return !suggestList.hidden && suggestItems.length > 0;
  }

  function closeSuggest() {
    suggestList.hidden = true;
    suggestList.textContent = "";
    suggestItems = [];
    suggestIndex = -1;
    suggestStart = -1;
  }

  function paintSuggest() {
    Array.prototype.forEach.call(suggestList.children, function (li, i) {
      li.setAttribute("aria-selected", i === suggestIndex ? "true" : "false");
    });
  }

  function chooseSuggest(i) {
    var note = suggestItems[i];
    if (!note || suggestStart < 0) {
      closeSuggest();
      return;
    }
    var cursor = text.selectionStart;
    var after = text.value.slice(cursor, cursor + 2) === "]]" ? 2 : 0;
    var insert = "[[" + (note.title || "") + "]]";
    var from = suggestStart;
    closeSuggest();
    replaceRange(from, cursor + after, insert, from + insert.length);
  }

  function handleSuggestKey(event) {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      suggestIndex = (suggestIndex + 1) % suggestItems.length;
      paintSuggest();
      return true;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      suggestIndex = (suggestIndex - 1 + suggestItems.length) % suggestItems.length;
      paintSuggest();
      return true;
    }
    if (event.key === "Enter" || event.key === "Tab") {
      event.preventDefault();
      chooseSuggest(suggestIndex < 0 ? 0 : suggestIndex);
      return true;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      closeSuggest();
      return true;
    }
    return false;
  }

  var lookupSuggest = (M.debounce || function (fn) { return fn; })(function (query, start) {
    var seq = ++suggestSeq;
    request("GET", "/api/suggest?limit=8&q=" + encodeURIComponent(query))
      .then(function (data) {
        if (seq !== suggestSeq) {
          return;
        }
        var notes = Array.isArray(data) ? data : (data && (data.notes || data.items)) || [];
        suggestList.textContent = "";
        suggestItems = notes;
        suggestStart = start;
        suggestIndex = notes.length ? 0 : -1;
        notes.forEach(function (note, i) {
          var li = el("li", "", note.title || "Untitled");
          li.setAttribute("role", "option");
          li.addEventListener("mousedown", function (event) {
            event.preventDefault();
            chooseSuggest(i);
          });
          suggestList.appendChild(li);
        });
        suggestList.hidden = notes.length === 0;
        paintSuggest();
      })
      .catch(closeSuggest);
  }, 150);

  text.addEventListener("input", function () {
    var cursor = text.selectionStart;
    var before = text.value.slice(Math.max(0, cursor - 80), cursor);
    var match = /\[\[([^\[\]\n|]{0,60})$/.exec(before);
    if (!match) {
      closeSuggest();
      return;
    }
    lookupSuggest(match[1], cursor - match[0].length);
  });

  text.addEventListener("blur", function () {
    setTimeout(closeSuggest, 150);
  });

  /* ---------- Attachments ---------- */

  function isImage(file) {
    return /^image\//.test(file.type || "");
  }

  function formatSize(n) {
    if (n < 1024) {
      return n + " B";
    }
    if (n < 1024 * 1024) {
      return (n / 1024).toFixed(n < 10240 ? 1 : 0) + " KB";
    }
    return (n / (1024 * 1024)).toFixed(1) + " MB";
  }

  /* uploadRow builds one progress row in the upload list. */
  function uploadRow(file) {
    var thumb = el("span", "upload-thumb");
    if (isImage(file) && window.URL && URL.createObjectURL) {
      var img = el("img");
      img.alt = "";
      img.src = URL.createObjectURL(file);
      img.onload = function () {
        URL.revokeObjectURL(img.src);
      };
      thumb.appendChild(img);
    }
    var name = el("span", "upload-name", file.name);
    var meta = el("span", "upload-meta", formatSize(file.size) + " · Waiting");
    var bar = el("span", "progress");
    bar.setAttribute("role", "progressbar");
    bar.setAttribute("aria-valuemin", "0");
    bar.setAttribute("aria-valuemax", "100");
    bar.setAttribute("aria-label", "Upload progress for " + file.name);
    var fill = el("span", "progress-fill");
    bar.appendChild(fill);
    var body = el("span", "upload-body");
    body.appendChild(name);
    body.appendChild(meta);
    body.appendChild(bar);
    var actions = el("span", "upload-actions");
    var row = el("li", "upload-row");
    row.setAttribute("data-state", "uploading");
    row.appendChild(thumb);
    row.appendChild(body);
    row.appendChild(actions);
    uploads.insertBefore(row, uploads.firstChild);
    return {
      progress: function (percent) {
        fill.style.width = percent + "%";
        bar.setAttribute("aria-valuenow", String(percent));
        meta.textContent = formatSize(file.size) + " · Uploading " + percent + "%";
      },
      done: function () {
        row.setAttribute("data-state", "done");
        bar.hidden = true;
        meta.textContent = formatSize(file.size) + " · Uploaded and inserted";
      },
      failed: function (message, retry) {
        row.setAttribute("data-state", "failed");
        bar.hidden = true;
        meta.textContent = "Failed: " + message;
        actions.textContent = "";
        var again = el("button", "btn small", "Retry");
        again.type = "button";
        again.addEventListener("click", function () {
          row.parentNode.removeChild(row);
          retry();
        });
        actions.appendChild(again);
      }
    };
  }

  /* Returns the folder part of an entry path. */
  function dirOf(path) {
    var i = String(path || "").lastIndexOf("/");
    return i < 0 ? "" : path.slice(0, i);
  }

  /* Builds the relative markdown destination from the note's folder to a file, as sync.js relativeDest does. */
  function relativeDest(fromDir, path) {
    var ds = fromDir ? fromDir.split("/") : [];
    var ps = path ? path.split("/") : [];
    var c = 0;
    while (c < ds.length && c < ps.length && ds[c] === ps[c]) {
      c++;
    }
    var parts = [];
    for (var k = c; k < ds.length; k++) {
      parts.push("..");
    }
    parts = parts.concat(ps.slice(c));
    return parts.map(function (seg) {
      return seg.replace(/%/g, "%25").replace(/ /g, "%20").replace(/\(/g, "%28").replace(/\)/g, "%29").replace(/</g, "%3C").replace(/>/g, "%3E");
    }).join("/") || ".";
  }

  function upload(file) {
    var row = uploadRow(file);
    var insertAt = text.selectionStart;
    var data = new FormData();
    // The text fields go before the file part, because the server streams the file as it arrives.
    // pasteFor saves the file in the note's folder, or in the space's paste folder.
    data.append("spaceId", spaceId);
    data.append("pasteFor", noteId);
    data.append("onConflict", "rename");
    data.append("file", file, file.name);
    var xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/files");
    xhr.withCredentials = true;
    xhr.setRequestHeader("Accept", "application/json");
    xhr.upload.addEventListener("progress", function (event) {
      if (event.lengthComputable) {
        row.progress(Math.round((event.loaded / event.total) * 100));
      }
    });
    xhr.addEventListener("load", function () {
      var res = null;
      try {
        res = JSON.parse(xhr.responseText);
      } catch (err) {
        res = null;
      }
      if (xhr.status < 200 || xhr.status >= 300 || !res) {
        row.failed((res && res.error) || xhr.statusText || "error", function () {
          upload(file);
        });
        return;
      }
      var saved = unwrap(res, "file");
      var name = saved.name || file.name;
      var notePath = form.getAttribute("data-note-path") || "";
      var url = saved.path ? relativeDest(dirOf(notePath), saved.path) : saved.url || "";
      var label = name.replace(/[\[\]]/g, "");
      var snippet = (isImage(file) || /^image\//.test(saved.mime || "") ? "!" : "") + "[" + label + "](" + url + ")";
      var pos = Math.min(insertAt, text.value.length);
      text.setSelectionRange(pos, pos);
      insertAtCursor(snippet);
      row.done();
    });
    xhr.addEventListener("error", function () {
      row.failed("the connection dropped", function () {
        upload(file);
      });
    });
    row.progress(0);
    xhr.send(data);
  }

  function uploadAll(files) {
    Array.prototype.forEach.call(files || [], upload);
  }

  text.addEventListener("dragover", function (event) {
    if (event.dataTransfer && Array.prototype.indexOf.call(event.dataTransfer.types, "Files") >= 0) {
      event.preventDefault();
      text.classList.add("dragging");
    }
  });

  text.addEventListener("dragleave", function () {
    text.classList.remove("dragging");
  });

  text.addEventListener("drop", function (event) {
    text.classList.remove("dragging");
    if (event.dataTransfer && event.dataTransfer.files && event.dataTransfer.files.length) {
      event.preventDefault();
      uploadAll(event.dataTransfer.files);
    }
  });

  text.addEventListener("paste", function (event) {
    var files = event.clipboardData && event.clipboardData.files;
    if (files && files.length) {
      event.preventDefault();
      uploadAll(files);
    }
  });

  if (uploadInput) {
    uploadInput.addEventListener("change", function () {
      uploadAll(uploadInput.files);
      uploadInput.value = "";
    });
  }

  var uploadZone = form.querySelector("[data-upload-zone]");
  if (uploadZone) {
    uploadZone.addEventListener("dragover", function (event) {
      if (event.dataTransfer && Array.prototype.indexOf.call(event.dataTransfer.types, "Files") >= 0) {
        event.preventDefault();
        uploadZone.classList.add("is-over");
      }
    });
    uploadZone.addEventListener("dragleave", function () {
      uploadZone.classList.remove("is-over");
    });
    uploadZone.addEventListener("drop", function (event) {
      uploadZone.classList.remove("is-over");
      if (event.dataTransfer && event.dataTransfer.files && event.dataTransfer.files.length) {
        event.preventDefault();
        uploadAll(event.dataTransfer.files);
      }
    });
  }

  /* ---------- AI panel ---------- */

  if (aiPanel) {
    var taskSelect = aiPanel.querySelector("[data-ai-task]");
    var providerSelect = aiPanel.querySelector("[data-ai-provider]");
    var instruction = aiPanel.querySelector("[data-ai-instruction]");
    var runButton = aiPanel.querySelector("[data-ai-run]");
    var result = aiPanel.querySelector("[data-ai-result]");
    var resultActions = aiPanel.querySelector("[data-ai-actions]");
    var question = aiPanel.querySelector("[data-ai-question]");
    var askButton = aiPanel.querySelector("[data-ai-ask]");
    var answer = aiPanel.querySelector("[data-ai-answer]");
    var sources = aiPanel.querySelector("[data-ai-sources]");
    var lastSelection = { start: 0, end: 0 };

    var rememberSelection = function () {
      lastSelection = { start: text.selectionStart, end: text.selectionEnd };
    };
    text.addEventListener("select", rememberSelection);
    text.addEventListener("keyup", rememberSelection);
    text.addEventListener("mouseup", rememberSelection);

    var streamAI = function (req, onDelta) {
      return fetch("/api/ai/stream", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        body: JSON.stringify(req)
      }).then(function (res) {
        if (!res.ok || !res.body) {
          return res.text().then(function (body) {
            var message = res.statusText;
            try {
              message = JSON.parse(body).error || message;
            } catch (err) {
              message = body || message;
            }
            throw new Error(message);
          });
        }
        var reader = res.body.getReader();
        var decoder = new TextDecoder();
        var buffer = "";
        var pump = function () {
          return reader.read().then(function (chunk) {
            if (chunk.done) {
              return;
            }
            buffer += decoder.decode(chunk.value, { stream: true });
            var events = buffer.split(/\r?\n\r?\n/);
            buffer = events.pop();
            for (var i = 0; i < events.length; i++) {
              var lines = events[i].split(/\r?\n/);
              var eventType = "message";
              var dataLines = [];
              lines.forEach(function (line) {
                if (line.indexOf("event:") === 0) {
                  eventType = line.slice(6).trim();
                } else if (line.indexOf("data:") === 0) {
                  dataLines.push(line.slice(5).replace(/^ /, ""));
                }
              });
              if (!dataLines.length) {
                continue;
              }
              var payload = dataLines.join("\n");
              var parsed = null;
              try {
                parsed = JSON.parse(payload);
              } catch (err) {
                parsed = { delta: payload };
              }
              if (eventType === "error" || (parsed && parsed.error)) {
                throw new Error((parsed && parsed.error) || "The model returned an error");
              }
              if (parsed && parsed.delta) {
                onDelta(parsed.delta);
              }
              if (parsed && parsed.done) {
                return reader.cancel();
              }
            }
            return pump();
          });
        };
        return pump();
      });
    };

    runButton.addEventListener("click", function () {
      rememberSelection();
      var selected = text.value.slice(lastSelection.start, lastSelection.end);
      result.textContent = "";
      resultActions.hidden = true;
      runButton.disabled = true;
      streamAI({
        task: taskSelect.value,
        noteId: noteId,
        spaceId: spaceId,
        text: selected || text.value,
        instruction: instruction.value,
        providerId: providerSelect ? providerSelect.value : ""
      }, function (delta) {
        result.textContent += delta;
      })
        .then(function () {
          resultActions.hidden = result.textContent === "";
        })
        .catch(function (err) {
          result.textContent = "AI request failed: " + (err.message || "error");
        })
        .then(function () {
          runButton.disabled = false;
        });
    });

    aiPanel.querySelector("[data-ai-insert]").addEventListener("click", function () {
      var pos = lastSelection.end;
      text.setSelectionRange(pos, pos);
      insertAtCursor(result.textContent);
    });

    aiPanel.querySelector("[data-ai-replace]").addEventListener("click", function () {
      replaceRange(lastSelection.start, lastSelection.end, result.textContent,
        lastSelection.start, lastSelection.start + result.textContent.length);
    });

    aiPanel.querySelector("[data-ai-copy]").addEventListener("click", function (event) {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(result.textContent).then(function () {
          event.target.textContent = "Copied";
        });
      }
    });

    askButton.addEventListener("click", function () {
      var q = question.value.trim();
      if (!q) {
        question.focus();
        return;
      }
      askButton.disabled = true;
      answer.textContent = "Thinking";
      sources.textContent = "";
      request("POST", "/api/ai/run", {
        task: "ask",
        question: q,
        useContext: true,
        spaceId: spaceId,
        noteId: noteId,
        providerId: providerSelect ? providerSelect.value : ""
      })
        .then(function (data) {
          var res = unwrap(data, "result");
          answer.textContent = res.text || "";
          (res.sources || []).forEach(function (src) {
            var li = el("li");
            var link = el("a", "", src.title || src.path || src.noteId);
            link.href = "/n/" + encodeURIComponent(src.noteId);
            li.appendChild(link);
            if (src.excerpt) {
              li.appendChild(el("div", "muted small", src.excerpt));
            }
            sources.appendChild(li);
          });
        })
        .catch(function (err) {
          answer.textContent = "AI request failed: " + (err.message || "error");
        })
        .then(function () {
          askButton.disabled = false;
        });
    });
  }

  setStatus("saved", "Saved");
})();
