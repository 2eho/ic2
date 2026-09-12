(function () {
  if (window.__IC2_OV_PILL__) return;
  window.__IC2_OV_PILL__ = true;

  var CHECK = "/update-api/check";
  var APPLY = "/update-api/apply";
  var STATUS = "/update-api/status";

  var root = document.createElement("div");
  root.id = "canvas-ov-pill";
  root.setAttribute("role", "status");
  root.innerHTML =
    '<span class="ver" id="canvas-ov-ver">STUDIO</span>' +
    '<span class="dot" id="canvas-ov-dot" hidden></span>' +
    '<button type="button" id="canvas-ov-btn" hidden>立即更新</button>' +
    '<span class="hint" id="canvas-ov-hint" hidden></span>';

  function mount() {
    if (!document.body) return false;
    if (!document.getElementById("canvas-ov-pill")) document.body.appendChild(root);
    return true;
  }

  var verEl = root.querySelector("#canvas-ov-ver");
  var dotEl = root.querySelector("#canvas-ov-dot");
  var btnEl = root.querySelector("#canvas-ov-btn");
  var hintEl = root.querySelector("#canvas-ov-hint");
  var last = null;
  var applying = false;

  function zh() {
    try { return !(window.StudioI18n && window.StudioI18n.lang && window.StudioI18n.lang() === "en"); }
    catch (e) { return true; }
  }

  function setHint(text) {
    if (!text) {
      hintEl.hidden = true;
      hintEl.textContent = "";
      return;
    }
    hintEl.hidden = false;
    hintEl.textContent = text;
  }

  function syncBadge(shortSha, title) {
    var badge = document.getElementById("project-version-badge");
    if (!badge || !shortSha) return;
    badge.textContent = shortSha;
    badge.title = title || ((zh() ? "当前 " : "Current ") + shortSha);
  }

  function applyState(data) {
    last = data || last;
    var short = (data && (data.local_short || data.short)) || "IC2";
    var remote = (data && (data.remote_short || "")) || "";
    var latest = !!(data && data.latest);
    verEl.textContent = short;
    root.title = remote
      ? ((zh() ? "当前 " : "Current ") + short + (zh() ? " / 上游 " : " / upstream ") + remote)
      : ((zh() ? "当前 " : "Current ") + short);
    syncBadge(short, root.title);
    if (!latest && remote && remote !== short) {
      dotEl.hidden = false;
      btnEl.hidden = false;
      if (!applying) {
        btnEl.disabled = false;
        btnEl.textContent = zh() ? "立即更新" : "Update now";
      }
    } else {
      dotEl.hidden = true;
      btnEl.hidden = true;
    }
  }

  function check(manual) {
    return fetch(CHECK, { cache: "no-store" })
      .then(function (r) { return r.json(); })
      .then(function (data) {
        applyState(data);
        if (manual) {
          if (data && data.latest) {
            setHint(zh() ? ("已是最新版本（" + (data.local_short || data.short || "") + "）") : ("Already latest (" + (data.local_short || data.short || "") + ")"));
            window.setTimeout(function () { setHint(""); }, 4000);
          } else if (data && data.remote_short) {
            setHint(zh() ? ("上游 " + data.remote_short) : ("Upstream " + data.remote_short));
          }
        }
        return data;
      })
      .catch(function () {
        if (!verEl.textContent) verEl.textContent = "IC2";
        if (manual) setHint(zh() ? "检测更新失败" : "Update check failed");
      });
  }

  function pollStatusThenReload() {
    var started = Date.now();
    function tick() {
      fetch(STATUS, { cache: "no-store" })
        .then(function (r) { return r.json(); })
        .then(function (st) {
          if (st && st.message) setHint(st.message);
          if (st && st.done) {
            if (st.ok === false) {
              applying = false;
              btnEl.disabled = false;
              btnEl.textContent = zh() ? "立即更新" : "Update now";
              setHint(st.error || st.message || (zh() ? "更新失败" : "Update failed"));
              return;
            }
            setHint(st.message || (zh() ? "更新完成，正在刷新…" : "Updated, reloading…"));
            window.setTimeout(function () { window.location.reload(); }, 1200);
            return;
          }
          if (Date.now() - started > 8 * 60 * 1000) {
            applying = false;
            btnEl.disabled = false;
            btnEl.textContent = zh() ? "立即更新" : "Update now";
            setHint(zh() ? "更新超时" : "Update timed out");
            return;
          }
          window.setTimeout(tick, 1000);
        })
        .catch(function () {
          if (Date.now() - started > 8 * 60 * 1000) {
            applying = false;
            btnEl.disabled = false;
            btnEl.textContent = zh() ? "立即更新" : "Update now";
            setHint(zh() ? "更新失败" : "Update failed");
            return;
          }
          window.setTimeout(tick, 1500);
        });
    }
    tick();
  }

  function applyNow() {
    if (applying) return;
    applying = true;
    btnEl.disabled = true;
    btnEl.textContent = zh() ? "正在更新..." : "Updating...";
    setHint(zh() ? "正在更新…" : "Updating…");
    fetch(APPLY, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
      cache: "no-store",
    })
      .then(function (r) {
        return r.json().then(function (data) {
          return { ok: r.ok, status: r.status, data: data || {} };
        }).catch(function () {
          return { ok: r.ok, status: r.status, data: {} };
        });
      })
      .then(function (res) {
        var data = res.data || {};
        if (data.latest && !data.started) {
          applying = false;
          btnEl.hidden = true;
          dotEl.hidden = true;
          btnEl.disabled = false;
          btnEl.textContent = zh() ? "立即更新" : "Update now";
          setHint(data.message || (zh() ? "已是最新版本" : "Already latest"));
          applyState(Object.assign({}, last || {}, data, { latest: true }));
          return;
        }
        if (!res.ok || data.ok === false) {
          throw new Error(data.message || ((zh() ? "更新失败 (" : "Update failed (") + res.status + ")"));
        }
        setHint(data.message || (zh() ? "已开始更新" : "Update started"));
        pollStatusThenReload();
      })
      .catch(function (err) {
        applying = false;
        btnEl.disabled = false;
        btnEl.textContent = zh() ? "立即更新" : "Update now";
        setHint(err && err.message ? err.message : (zh() ? "更新失败" : "Update failed"));
      });
  }

  btnEl.addEventListener("click", applyNow);

  window.checkForUpdates = function (manual) {
    check(!!manual);
  };
  window.runProjectUpdate = function () {
    if (last && last.latest) {
      setHint(zh() ? ("已是最新版本（" + (last.local_short || last.short || "") + "）") : ("Already latest (" + (last.local_short || last.short || "") + ")"));
      return;
    }
    applyNow();
  };


  // Draggable; remembers position. Default CSS is top-left.
  var POS_KEY = "ic2-update-pill-pos";
  function clamp(n, min, max) { return Math.max(min, Math.min(max, n)); }
  function applyPos(x, y) {
    var pad = 8;
    var w = root.offsetWidth || 120;
    var h = root.offsetHeight || 32;
    x = clamp(x, pad, Math.max(pad, window.innerWidth - w - pad));
    y = clamp(y, pad, Math.max(pad, window.innerHeight - h - pad));
    root.style.left = x + "px";
    root.style.top = y + "px";
    root.style.right = "auto";
    root.style.bottom = "auto";
    return { x: x, y: y };
  }
  function restorePos() {
    try {
      var raw = localStorage.getItem(POS_KEY);
      if (!raw) return;
      var p = JSON.parse(raw);
      if (typeof p.x === "number" && typeof p.y === "number") applyPos(p.x, p.y);
    } catch (e) {}
  }
  function savePos(p) {
    try { localStorage.setItem(POS_KEY, JSON.stringify(p)); } catch (e) {}
  }
  function enableDrag() {
    var dragging = false;
    var moved = false;
    var startX = 0, startY = 0, origX = 0, origY = 0;
    function onDown(ev) {
      if (ev.target && ev.target.closest && ev.target.closest("button")) return;
      var pt = ev.touches ? ev.touches[0] : ev;
      dragging = true;
      moved = false;
      root.classList.add("dragging");
      startX = pt.clientX;
      startY = pt.clientY;
      var rect = root.getBoundingClientRect();
      origX = rect.left;
      origY = rect.top;
      ev.preventDefault();
    }
    function onMove(ev) {
      if (!dragging) return;
      var pt = ev.touches ? ev.touches[0] : ev;
      var dx = pt.clientX - startX;
      var dy = pt.clientY - startY;
      if (Math.abs(dx) + Math.abs(dy) > 3) moved = true;
      applyPos(origX + dx, origY + dy);
      if (ev.cancelable) ev.preventDefault();
    }
    function onUp() {
      if (!dragging) return;
      dragging = false;
      root.classList.remove("dragging");
      if (moved) {
        var rect = root.getBoundingClientRect();
        savePos(applyPos(rect.left, rect.top));
      }
    }
    root.addEventListener("mousedown", onDown);
    root.addEventListener("touchstart", onDown, { passive: false });
    window.addEventListener("mousemove", onMove, { passive: false });
    window.addEventListener("touchmove", onMove, { passive: false });
    window.addEventListener("mouseup", onUp);
    window.addEventListener("touchend", onUp);
    window.addEventListener("resize", function () {
      var rect = root.getBoundingClientRect();
      savePos(applyPos(rect.left, rect.top));
    });
    root.addEventListener("click", function (ev) {
      if (moved && !(ev.target && ev.target.closest && ev.target.closest("button"))) {
        ev.stopPropagation();
        moved = false;
      }
    }, true);
  }
  function bootPill() {
    mount();
    restorePos();
    enableDrag();
  }
  if (!bootPill()) document.addEventListener("DOMContentLoaded", bootPill);

  check(false);
  fetch(STATUS, { cache: "no-store" })
    .then(function (r) { return r.json(); })
    .then(function (st) {
      if (st && st.running && !st.done) {
        applying = true;
        btnEl.hidden = false;
        btnEl.disabled = true;
        btnEl.textContent = zh() ? "正在更新..." : "Updating...";
        setHint(st.message || (zh() ? "正在更新…" : "Updating…"));
        pollStatusThenReload();
      }
    })
    .catch(function () {});
})();
