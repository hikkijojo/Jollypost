const state = {
  me: null,
  products: [],
  categories: [],
  week: null,
  weekOffset: 0,
  tab: "week",
  showDone: false,
  pendingTaskId: "",
  openTaskId: null,
  slideTaskId: null,
};

const proofBlobs = new Map();

class Forbidden extends Error {
  constructor(data) {
    super("forbidden");
    this.data = data;
  }
}

class Unauthorized extends Error {
  constructor() {
    super("unauthorized");
  }
}

const app = document.querySelector("#app");

boot().catch((err) => {
  if (err instanceof Unauthorized) {
    showLogin();
    return;
  }
  if (err instanceof Forbidden) {
    showForbidden(err.data);
    return;
  }
  resetChrome();
  document.querySelector("#tabs").hidden = true;
  document.querySelector("#who").textContent = "";
  app.innerHTML = `<section class="panel gate"><h2>打不開</h2><p class="lead">${esc(err.message || "打不開")}</p></section>`;
  app.setAttribute("aria-busy", "false");
});

async function boot() {
  const liffId = document.body.dataset.liffId || "";
  if (!liffId || !window.liff) {
    throw new Error("LIFF 還沒設定好");
  }
  await liff.init({ liffId });
  if (!liff.isLoggedIn()) {
    liff.login({ redirectUri: loginRedirect() });
    return;
  }
  await reload();
  bindTabs();
  render();
}

function bindTabs() {
  document.querySelectorAll("#tabs button").forEach((btn) => {
    btn.onclick = () => {
      state.tab = btn.dataset.tab;
      render();
    };
  });
}

async function reload() {
  state.me = await api("GET", "/api/me");
  state.products = await api("GET", "/api/products");
  state.categories = state.me.admin ? await api("GET", "/api/categories") : [];
  state.week = await api("GET", "/api/tasks?week=" + state.weekOffset);
}

async function token() {
  const value = liff.getIDToken();
  if (!value) throw new Unauthorized();
  return value;
}

function loginRedirect() {
  return location.origin + location.pathname;
}

function relogin() {
  const redirectUri = loginRedirect();
  try {
    if (window.liff && liff.isLoggedIn()) liff.logout();
  } catch {
    // 舊的登入清不掉也繼續要一組新的。
  }
  liff.login({ redirectUri });
}

async function api(method, path, body) {
  const headers = { Authorization: "Bearer " + (await token()) };
  const opts = { method, headers };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  const data = await res.json().catch(() => ({}));
  if (res.status === 401) throw new Unauthorized();
  if (res.status === 403 && data.userId) throw new Forbidden(data);
  if (!res.ok) throw new Error(data.error?.message || "請求失敗");
  return data.data;
}

function render() {
  document.querySelector("#tabs").hidden = false;
  document.querySelector('[data-tab="products"]').hidden = !state.me.admin;
  document.querySelector('[data-tab="members"]').hidden = !state.me.admin;
  if (!state.me.admin && (state.tab === "products" || state.tab === "members")) state.tab = "week";
  document.querySelector("#who").textContent = state.me.displayName || "";
  document.querySelectorAll("#tabs button").forEach((btn) => {
    const on = btn.dataset.tab === state.tab;
    btn.classList.toggle("active", on);
    if (on) btn.setAttribute("aria-current", "page");
    else btn.removeAttribute("aria-current");
  });
  if (state.tab === "inbox") renderInbox();
  if (state.tab === "week") renderWeek();
  if (state.tab === "products") renderProducts();
  if (state.tab === "members") renderMembers();
  syncChrome();
  app.setAttribute("aria-busy", "false");
}

function renderInbox() {
  app.innerHTML = `
    <section class="panel">
      <p class="eyebrow">收任務</p>
      <h2>貼上老闆的訊息</h2>
      <p class="lead">一段可以有多則。編號和貼文連結寫在一起，產品名稱可寫在連結下一行。</p>
      <div class="sample">
        <p class="sample-label">範例</p>
        <pre>10/11-3
https://www.threads.com/share/xxxx/

https://www.threads.com/share/xxxx/
Jolly 床邊床</pre>
      </div>
      <label for="paste">訊息</label>
      <textarea id="paste" placeholder="整段貼上"></textarea>
      <div class="actions">
        <button type="button" class="wide" id="import">寫入當月任務</button>
      </div>
    </section>`;
  document.querySelector("#import").onclick = () => guard(importTasks);
}

async function importTasks() {
  const text = document.querySelector("#paste").value;
  const result = await api("POST", "/api/tasks/import", { text });
  const dup = (result.tasks || []).some((task) => task.duplicate);
  toast(dup ? `已寫入 ${result.tasks.length} 則，其中有連結這個月重複` : `已寫入 ${result.tasks.length} 則`);
  state.weekOffset = 0;
  state.showDone = false;
  state.tab = "week";
  await reload();
  render();
}

function renderWeek() {
  state.slideGeneration = (state.slideGeneration || 0) + 1;
  const week = state.week;
  const all = week.tasks || [];
  const openTasks = all.filter((task) => task.status !== "submitted");
  const doneTasks = all.filter((task) => task.status === "submitted");
  const tasks = state.showDone ? doneTasks : openTasks;
  const emptyKind = all.length === 0 ? "none" : state.showDone ? "done" : "open";
  const openTask = all.find((task) => task.id === state.openTaskId) || null;
  if (!openTask) {
    state.openTaskId = null;
    state.slideTaskId = null;
  }
  const animate = openTask && state.slideTaskId !== openTask.id;
  app.innerHTML = `
    <div class="stage-clip">
      <div class="stage ${openTask && !animate ? "open" : ""}">
        <div class="stage-list">
          <section class="panel">
            <div class="segment" role="group" aria-label="選擇篇章">
              <button type="button" id="month-prev" class="${week.week >= 0 ? "" : "is-on"}" aria-pressed="${week.week < 0}">上月</button>
              <button type="button" id="month-current" class="${week.week >= 0 ? "is-on" : ""}" aria-pressed="${week.week >= 0}">當月</button>
            </div>
            <h2 class="month-title">${esc(week.label)}</h2>
            <p class="month-count">共 ${week.total} 則</p>
            ${meter(week)}
            <div class="segment stack-gap" role="group" aria-label="任務狀態">
              <button type="button" id="filter-open" class="${state.showDone ? "" : "is-on"}" aria-pressed="${!state.showDone}">待回 ${openTasks.length}</button>
              <button type="button" id="filter-done" class="${state.showDone ? "is-on" : ""}" aria-pressed="${state.showDone}">已回 ${doneTasks.length}</button>
            </div>
            ${week.total ? roster(week.byUser) : ""}
          </section>
          ${tasks.length ? tasks.map(taskPreview).join("") : emptyState(emptyKind)}
        </div>
        <div class="stage-detail">
          ${openTask ? taskCard(openTask) : ""}
        </div>
      </div>
    </div>`;
  if (animate) {
    state.slideTaskId = openTask.id;
    scrollDetailToTop();
    requestAnimationFrame(() => {
      const stage = app.querySelector(".stage");
      if (stage) stage.classList.add("open");
      scrollDetailToTop();
      syncStageA11y();
    });
  } else if (!openTask && state.restoreListScroll) {
    const y = state.listScroll || 0;
    state.restoreListScroll = false;
    requestAnimationFrame(() => window.scrollTo(0, y));
  }
  document.querySelector("#month-prev").onclick = () => guard(() => moveMonth(-1));
  document.querySelector("#month-current").onclick = () => guard(() => moveMonth(0));
  document.querySelector("#filter-open").onclick = () => {
    state.showDone = false;
    render();
  };
  document.querySelector("#filter-done").onclick = () => {
    state.showDone = true;
    render();
  };
  app.querySelectorAll("[data-open-task]").forEach((btn) => {
    btn.onclick = () => {
      state.listScroll = window.scrollY || document.documentElement.scrollTop || document.body.scrollTop || 0;
      state.openTaskId = Number(btn.dataset.openTask);
      render();
    };
  });
  syncStageA11y();
  bindDetailSwipe();
  app.querySelectorAll("[data-open]").forEach((btn) => {
    btn.onclick = () => openPost(btn.dataset.open);
  });
  app.querySelectorAll("[data-product-search]").forEach((input) => {
    input.oninput = () => refreshProductChoices(input.dataset.productSearch);
  });
  app.querySelectorAll("[data-category]").forEach((sel) => {
    sel.onchange = () => refreshProductChoices(sel.dataset.category);
  });
  app.querySelectorAll("[data-product]").forEach((sel) => {
    sel.onchange = () => guard(() => changeProduct(sel.dataset.product, sel.value));
  });
  app.querySelectorAll("[data-copy]").forEach((btn) => {
    btn.onclick = () => guard(() => copyReply(btn.dataset.copy, btn.dataset.text));
  });
  app.querySelectorAll("[data-upload]").forEach((btn) => {
    btn.onclick = () => {
      state.pendingTaskId = btn.dataset.upload;
      const dialog = document.querySelector("#account");
      dialog.hidden = false;
      lockPageScroll(true);
      const first = dialog.querySelector(".account-grid button");
      if (first) first.focus({ preventScroll: true });
    };
  });
  app.querySelectorAll("[data-file]").forEach((input) => {
    input.onchange = () => {
      const file = input.files && input.files[0];
      const account = input.dataset.account || "";
      input.value = "";
      if (file) guard(() => uploadProof(input.dataset.file, file, account));
    };
  });
  app.querySelectorAll("[data-download]").forEach((btn) => {
    btn.onclick = () => guard(() => downloadProof(btn.dataset.download, btn.dataset.code));
  });
  app.querySelectorAll("[data-delete]").forEach((btn) => {
    btn.onclick = () => guard(() => removeTask(btn.dataset.delete));
  });
  app.querySelectorAll("[data-proof]").forEach((img) => {
    loadProof(img);
  });
  loadThreadPreviews();
}

function slideTaskClosed() {
  const stage = app.querySelector(".stage");
  if (!stage || !stage.classList.contains("open")) {
    state.openTaskId = null;
    state.slideTaskId = null;
    render();
    return;
  }
  let closed = false;
  const generation = state.slideGeneration;
  const done = () => {
    if (closed || generation !== state.slideGeneration) return;
    closed = true;
    state.restoreListScroll = true;
    state.openTaskId = null;
    state.slideTaskId = null;
    render();
  };
  const timer = setTimeout(done, 400);
  stage.addEventListener("transitionend", (event) => {
    if (event.target !== stage || event.propertyName !== "transform") return;
    clearTimeout(timer);
    done();
  }, { once: true });
  stage.classList.remove("open");
}

function taskPreview(task) {
  const product = state.products.find((item) => item.id === task.productId);
  const meta = statusMeta(task.status);
  const title = product ? product.name : (task.productText || "尚未選產品");
  return `
    <button type="button" class="preview" data-open-task="${task.id}">
      <span class="preview-media" data-thread="${task.id}">
        <img class="thread-photo" alt="" hidden>
        <span class="thread-fallback" aria-hidden="true"></span>
      </span>
      <span class="preview-main">
        <span class="preview-top">
          <span class="code">${esc(task.code)}</span>
          <span class="badge ${meta.badge}">${esc(meta.label)}</span>
        </span>
        <span class="preview-title">${esc(title)}</span>
        <span class="thread-author"></span>
        <span class="thread-body"><span class="skel"></span><span class="skel short"></span></span>
        <span class="muted">${esc(task.createdByName)} · ${esc(shortTime(task.createdAt))}</span>
        ${task.replyText ? `<span class="preview-reply">${esc(task.replyText)}</span>` : ""}
      </span>
    </button>`;
}

function threadBlock(taskId, extra) {
  return `
    <span class="thread ${extra}" data-thread="${taskId}">
      <img class="thread-photo" alt="" hidden>
      <span class="thread-fallback" aria-hidden="true"></span>
      <span class="thread-copy">
        <span class="thread-author"></span>
        <span class="thread-body"><span class="skel"></span><span class="skel short"></span></span>
      </span>
    </span>`;
}

function taskCard(task) {
  const product = state.products.find((item) => item.id === task.productId);
  const lines = product ? product.lines.map((line) => applyProductName(line, product.name)) : [];
  const meta = statusMeta(task.status);
  const replies = `
      <div class="picker" data-picker="${task.id}">
        <div>
          <label for="search-${task.id}">搜尋</label>
          <input id="search-${task.id}" type="search" data-product-search="${task.id}" placeholder="輸入產品名稱" autocomplete="off" enterkeyhint="search">
        </div>
        <div>
          <label for="cat-${task.id}">分類</label>
          <select id="cat-${task.id}" data-category="${task.id}">
            ${categoryOptions(product ? product.categoryId : "")}
          </select>
        </div>
        <div>
          <label for="prod-${task.id}">產品</label>
          <select id="prod-${task.id}" data-product="${task.id}">
            ${productOptions(task.productId, product ? product.categoryId : "", "")}
          </select>
        </div>
      </div>
      ${lines.length ? `<p class="copy-hint">點一句就會複製</p>` + lines.map((line) => `<button type="button" class="reply" data-copy="${task.id}" data-text="${esc(line)}">${esc(line)}</button>`).join("") : `<p class="muted">先選擇產品，才看得到這個類型的話術。</p>`}`;
  const replyBlock = task.status === "submitted"
    ? `<details class="fold"><summary>推薦話術</summary>${replies}</details>`
    : replies;
  return `
    <article class="card">
      <p class="code">${esc(task.code)}</p>
      <p class="badge-row">
        <span class="badge ${meta.badge}">${esc(meta.label)}</span>
      </p>
      ${task.duplicate ? `<p class="callout">這個月已經有人收過這則連結。</p>` : ""}
      ${task.unmatched ? `<p class="callout">清單裡沒有「${esc(task.productText)}」，請手動選產品。</p>` : ""}
      ${task.replyText ? `<p class="mine"><span>我的回文</span>${esc(task.replyText)}</p>` : ""}
      <p class="meta">${esc(task.createdByName)} · ${esc(shortTime(task.createdAt))}${product ? " · " + esc(product.categoryName) : ""}</p>
      ${threadBlock(task.id, "detail")}
      <div class="actions">
        <button type="button" class="ghost wide" data-open="${esc(task.url)}">打開貼文</button>
      </div>
      ${replyBlock}
      <div class="submit-row">
        <button type="button" data-upload="${task.id}">上傳截圖押編號</button>
        <input data-file="${task.id}" type="file" accept="image/*" hidden>
        <button type="button" class="text-danger" data-delete="${task.id}">刪除任務</button>
      </div>
      ${task.hasProof ? `<img class="proof" alt="已押上 ${esc(task.code)}" data-proof="/api/tasks/${task.id}/proof" data-proof-id="${task.id}"><button type="button" class="save" data-download="${task.id}" data-code="${esc(task.code)}">存到相簿</button>` : ""}
    </article>`;
}

function categoryOptions(selectedId) {
  const seen = new Set();
  const categories = [];
  for (const item of state.products) {
    if (seen.has(item.categoryId)) continue;
    seen.add(item.categoryId);
    categories.push(item);
  }
  return `<option value="">全部分類</option>` + categories.map((item) => `
    <option value="${item.categoryId}" ${item.categoryId === selectedId ? "selected" : ""}>${esc(item.categoryName || "未分類")}</option>`).join("");
}

function productOptions(selectedId, categoryId, query) {
  const narrowed = Boolean(categoryId || String(query || "").trim());
  const items = narrowed
    ? filteredProducts(categoryId, query)
    : state.products.filter((item) => item.id === selectedId);
  const placeholder = !narrowed
    ? "請先選分類或輸入名稱"
    : items.length
      ? `請選擇（${items.length}）`
      : "沒有符合的產品";
  const showCategory = narrowed && !categoryId;
  return `<option value="">${esc(placeholder)}</option>` + items.map((item) => {
    const label = showCategory && item.categoryName ? `${item.name}（${item.categoryName}）` : item.name;
    return `<option value="${item.id}" ${item.id === selectedId ? "selected" : ""}>${esc(label)}</option>`;
  }).join("");
}

function filteredProducts(categoryId, query) {
  const q = normalizeQuery(query);
  return state.products.filter((item) => {
    if (categoryId && String(item.categoryId) !== String(categoryId)) return false;
    if (!q) return true;
    return normalizeQuery(item.name).includes(q);
  });
}

function normalizeQuery(value) {
  return String(value || "").trim().toLocaleLowerCase().replace(/\s+/g, "");
}

function refreshProductChoices(taskId) {
  const picker = app.querySelector(`[data-picker="${taskId}"]`);
  if (!picker) return;
  const select = picker.querySelector("[data-product]");
  const categoryId = picker.querySelector("[data-category]").value;
  const query = picker.querySelector("[data-product-search]").value;
  const current = Number(select.value) || 0;
  select.innerHTML = productOptions(current, categoryId, query);
}

function applyProductName(line, name) {
  return String(line || "").split("{產品}").join(name || "這款");
}

function scrollDetailToTop() {
  const header = document.querySelector("header");
  const headerH = header ? header.offsetHeight : 0;
  const detail = app.querySelector(".stage-detail");
  if (!detail) {
    window.scrollTo(0, 0);
    return;
  }
  const y = detail.getBoundingClientRect().top + (window.scrollY || document.documentElement.scrollTop || 0);
  window.scrollTo(0, Math.max(0, y - headerH - 4));
}

function statusMeta(status) {
  if (status === "submitted") return { label: "已交圖", badge: "ok" };
  if (status === "copied") return { label: "已複製", badge: "progress" };
  if (status === "pending") return { label: "待回", badge: "" };
  return { label: status || "", badge: "" };
}

function meter(week) {
  const total = Number(week.total) || 0;
  if (!total) return "";
  const done = Number(week.submitted) || 0;
  const width = Math.round((done / total) * 100);
  return `
    <div class="meter">
      <div class="meter-row"><span>我的進度</span><b>${done}/${total}</b></div>
      <div class="meter-track" role="progressbar" aria-valuemin="0" aria-valuemax="${total}" aria-valuenow="${done}" aria-label="我的回文進度">
        <span style="width:${width}%"></span>
      </div>
    </div>`;
}

function roster(people) {
  const rows = people || [];
  if (!rows.length) return "";
  return `
    <details class="roster-fold">
      <summary>大家的進度</summary>
      <ul class="roster">${rows.map((person) => `<li><span>${esc(person.name)}</span><b>${person.submitted}<span>/${person.total}</span></b></li>`).join("")}</ul>
    </details>`;
}

function emptyState(kind) {
  const copy = {
    none: ["這個月還沒有任務", "到「收任務」貼上老闆的訊息，就會列在這裡。"],
    open: ["待回的都處理完了", "可以切到「已回」，查看交過圖的任務。"],
    done: ["還沒有已回的任務", "複製話術或上傳截圖之後，會出現在這裡。"],
    types: ["還沒有類型", "話術類型會出現在這裡。"],
  };
  const item = copy[kind] || copy.none;
  return `<div class="empty"><p class="empty-title">${item[0]}</p><p>${item[1]}</p></div>`;
}

function syncChrome() {
  const header = document.querySelector("header");
  const openTask = state.tab === "week" && state.week
    ? (state.week.tasks || []).find((task) => task.id === state.openTaskId)
    : null;
  let back = header.querySelector(".header-back");
  if (openTask) {
    if (!back) {
      back = document.createElement("button");
      back.type = "button";
      back.className = "header-back";
      back.textContent = "返回";
      back.setAttribute("aria-label", "返回列表");
      header.querySelector(".brand-lockup").before(back);
    }
    back.onclick = () => slideTaskClosed();
    header.classList.add("has-back");
    header.querySelector(".brand").textContent = "編號";
    header.querySelector("h1").textContent = openTask.code;
  } else {
    if (back) back.remove();
    header.classList.remove("has-back");
    header.querySelector(".brand").textContent = "璟豐";
    header.querySelector("h1").textContent = "PO文小多比";
  }
  document.documentElement.style.setProperty("--header-h", header.offsetHeight + "px");
}

function resetChrome() {
  state.openTaskId = null;
  state.slideTaskId = null;
  const header = document.querySelector("header");
  const back = header.querySelector(".header-back");
  if (back) back.remove();
  header.classList.remove("has-back");
  header.querySelector(".brand").textContent = "璟豐";
  header.querySelector("h1").textContent = "PO文小多比";
}

function syncStageA11y() {
  const stage = app.querySelector(".stage");
  if (!stage) return;
  const list = stage.querySelector(".stage-list");
  const detail = stage.querySelector(".stage-detail");
  const open = stage.classList.contains("open");
  if (list) {
    list.toggleAttribute("inert", open);
    list.setAttribute("aria-hidden", open ? "true" : "false");
  }
  if (detail) {
    detail.toggleAttribute("inert", !open);
    detail.setAttribute("aria-hidden", open ? "false" : "true");
  }
}

function bindDetailSwipe() {
  const detail = app.querySelector(".stage-detail");
  if (!detail) return;
  let startX = 0;
  let startY = 0;
  let tracking = false;
  detail.addEventListener("touchstart", (event) => {
    if (!app.querySelector(".stage.open")) return;
    const touch = event.changedTouches[0];
    startX = touch.clientX;
    startY = touch.clientY;
    tracking = startX < 32;
  }, { passive: true });
  detail.addEventListener("touchend", (event) => {
    if (!tracking) return;
    tracking = false;
    const touch = event.changedTouches[0];
    const dx = touch.clientX - startX;
    const dy = touch.clientY - startY;
    if (dx > 72 && Math.abs(dx) > Math.abs(dy) * 1.2) slideTaskClosed();
  }, { passive: true });
}

async function moveMonth(offset) {
  if (state.weekOffset === offset) return;
  state.openTaskId = null;
  state.slideTaskId = null;
  state.restoreListScroll = false;
  state.weekOffset = offset;
  state.week = await api("GET", "/api/tasks?week=" + state.weekOffset);
  render();
}

async function changeProduct(taskId, productId) {
  if (!productId) return;
  await api("PATCH", "/api/tasks/" + taskId, { productId: Number(productId) });
  state.week = await api("GET", "/api/tasks?week=" + state.weekOffset);
  render();
}

async function copyReply(taskId, text) {
  await writeClipboard(text);
  await api("POST", "/api/tasks/" + taskId + "/copy", { replyText: text });
  toast("已複製");
  state.week = await api("GET", "/api/tasks?week=" + state.weekOffset);
  render();
}

async function uploadProof(taskId, file, account) {
  showBusy("正在押上編號…");
  try {
    const body = new FormData();
    body.append("file", file);
    body.append("account", account);
    const res = await fetch("/api/tasks/" + taskId + "/proof", {
      method: "POST",
      headers: { Authorization: "Bearer " + (await token()) },
      body,
    });
    const data = await res.json().catch(() => ({}));
    if (res.status === 401) throw new Unauthorized();
    if (res.status === 403 && data.userId) throw new Forbidden(data);
    if (!res.ok) throw new Error(data.error?.message || "上傳失敗");
    toast("已押上日期");
    state.week = await api("GET", "/api/tasks?week=" + state.weekOffset);
    render();
  } finally {
    hideBusy();
  }
}

async function removeTask(taskId) {
  if (!confirm("刪掉後大家都會看不到這則，這個編號也不會再拿去用。")) return;
  await api("DELETE", "/api/tasks/" + taskId);
  toast("已刪除");
  state.week = await api("GET", "/api/tasks?week=" + state.weekOffset);
  render();
}

const threadCards = new Map();
const threadLoading = new Set();

function loadThreadPreviews() {
  const ids = [...new Set([...app.querySelectorAll("[data-thread]")].map((node) => node.dataset.thread))];
  for (const id of ids) {
    const cached = threadCards.get(id);
    if (cached) paintThread(id, cached);
  }
  const queue = ids.filter((id) => !threadCards.has(id) && !threadLoading.has(id));
  const worker = async () => {
    while (queue.length) {
      const id = queue.shift();
      await fetchThread(id);
    }
  };
  worker();
  worker();
  worker();
}

async function fetchThread(id) {
  if (threadCards.has(id) || threadLoading.has(id)) return;
  threadLoading.add(id);
  let data = { author: "", body: "", hasImage: false };
  try {
    data = await api("GET", "/api/tasks/" + id + "/thread");
  } catch (err) {
    threadLoading.delete(id);
    if (err instanceof Unauthorized) {
      showLogin();
      return;
    }
    paintThread(id, { author: "", body: "這則貼文暫時讀不到", photo: "" });
    return;
  }
  let photo = "";
  if (data.hasImage) {
    try {
      const res = await fetch("/api/tasks/" + id + "/thread-image", {
        headers: { Authorization: "Bearer " + (await token()) },
      });
      if (res.status === 401) {
        threadLoading.delete(id);
        showLogin();
        return;
      }
      if (res.ok) photo = URL.createObjectURL(await res.blob());
    } catch (err) {
      if (err instanceof Unauthorized) {
        threadLoading.delete(id);
        showLogin();
        return;
      }
    }
  }
  threadLoading.delete(id);
  const card = { author: data.author || "", body: data.body || "", photo };
  threadCards.set(id, card);
  paintThread(id, card);
}

function paintThread(id, card) {
  app.querySelectorAll(`[data-thread="${CSS.escape(id)}"]`).forEach((node) => {
    const scope = node.classList.contains("preview-media") ? node.parentElement : node;
    const author = scope.querySelector(".thread-author");
    const body = scope.querySelector(".thread-body");
    const photo = node.querySelector(".thread-photo");
    const fallback = node.querySelector(".thread-fallback");
    const name = card.author || "Threads";
    if (author) author.textContent = name;
    if (body) body.textContent = card.body || "這則貼文暫時讀不到";
    if (photo && card.photo) {
      photo.src = card.photo;
      photo.alt = card.author ? card.author + "的貼文圖片" : "貼文圖片";
      photo.hidden = false;
      if (fallback) fallback.textContent = "";
    } else if (fallback) {
      fallback.textContent = name.slice(0, 1);
    }
  });
}

async function loadProof(img) {
  let res;
  try {
    res = await fetch(img.dataset.proof, {
      headers: { Authorization: "Bearer " + (await token()) },
    });
  } catch (err) {
    if (err instanceof Unauthorized) showLogin();
    return;
  }
  if (res.status === 401) {
    showLogin();
    return;
  }
  if (!res.ok) return;
  const blob = await res.blob();
  const id = String(img.dataset.proofId || "");
  const previous = proofBlobs.get(id);
  if (previous) URL.revokeObjectURL(previous.url);
  const url = URL.createObjectURL(blob);
  proofBlobs.set(id, { blob, url });
  img.src = url;
}

async function downloadProof(taskId, code) {
  let blob = proofBlobs.get(String(taskId))?.blob;
  if (!blob) {
    showBusy("正在準備圖片…");
    try {
      const res = await fetch("/api/tasks/" + taskId + "/proof", {
        headers: { Authorization: "Bearer " + (await token()) },
      });
      if (res.status === 401) throw new Unauthorized();
      if (!res.ok) throw new Error("下載失敗");
      blob = await res.blob();
    } finally {
      hideBusy();
    }
  }
  await saveToPhotos(blob, code);
}

async function saveToPhotos(blob, code) {
  const name = String(code || "截圖").replace(/[\\/:*?"<>|]/g, "-") + ".jpg";
  const file = new File([blob], name, { type: blob.type || "image/jpeg" });
  if (navigator.canShare && navigator.canShare({ files: [file] })) {
    try {
      await navigator.share({ files: [file] });
      return;
    } catch (err) {
      if (err && err.name === "AbortError") return;
    }
  }
  if (isIOS()) {
    toast("請長按上面的圖片，選「儲存影像」。");
    return;
  }
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 2000);
  toast("已開始下載");
}

function isIOS() {
  return /iPad|iPhone|iPod/.test(navigator.userAgent)
    || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
}

function showBusy(message) {
  const el = document.querySelector("#busy");
  el.hidden = false;
  el.querySelector("p").textContent = message || "處理中…";
  lockPageScroll(true);
}

function hideBusy() {
  document.querySelector("#busy").hidden = true;
  lockPageScroll(false);
}

function lockPageScroll(on) {
  document.body.style.overflow = on ? "hidden" : "";
}

async function renderMembers() {
  const gen = (state.memberGen || 0) + 1;
  state.memberGen = gen;
  app.innerHTML = `<section class="panel"><p class="muted">讀取名單…</p></section>`;
  try {
    const data = await api("GET", "/api/members");
    if (gen !== state.memberGen || state.tab !== "members") return;
    const admins = data.admins || [];
    const members = data.members || [];
    app.innerHTML = `
      <section class="panel">
        <p class="eyebrow">成員</p>
        <h2>可以使用的人</h2>
        <p class="lead">一般使用者可以在這裡加入或移除。管理者列在最上面，調整要改伺服器的 LINE_ADMIN_USER_IDS。</p>
        <h3 class="list-label">管理者 ${admins.length}</h3>
        <ul class="product-list member-list">${admins.map((person) => personRow(person, false)).join("")}</ul>
        <h3 class="list-label">一般使用者 ${members.length}</h3>
        ${members.length ? `<ul class="product-list member-list">${members.map((person) => personRow(person, true)).join("")}</ul>` : `<p class="muted">還沒有一般使用者。</p>`}
      </section>
      <section class="panel">
        <h2>加入一般使用者</h2>
        <form id="add-member">
          <label for="member-id">LINE userId</label>
          <input id="member-id" name="userId" type="text" required autocomplete="off" spellcheck="false" placeholder="U 開頭的 userId">
          <label for="member-name">怎麼稱呼</label>
          <input id="member-name" name="displayName" type="text" autocomplete="off" placeholder="可空白，對方登入後會補上">
          <div class="actions"><button type="submit" class="wide">加入名單</button></div>
        </form>
      </section>`;
    const form = document.querySelector("#add-member");
    form.onsubmit = (event) => {
      event.preventDefault();
      guard(() => addMember(form));
    };
    app.querySelectorAll("[data-remove-member]").forEach((btn) => {
      btn.onclick = () => guard(() => removeMember(btn.dataset.removeMember));
    });
  } catch (err) {
    if (gen !== state.memberGen || state.tab !== "members") return;
    if (err instanceof Unauthorized) {
      showLogin();
      return;
    }
    if (err instanceof Forbidden) {
      showForbidden(err.data);
      return;
    }
    app.innerHTML = `<section class="panel gate"><h2>名單打不開</h2><p class="lead">${esc(err.message || "讀取失敗")}</p></section>`;
  }
}

function personRow(person, removable) {
  const name = person.displayName || "尚未有稱呼";
  const action = removable
    ? `<button type="button" class="text-danger" data-remove-member="${esc(person.userId)}">移除</button>`
    : `<span class="badge">管理者</span>`;
  return `<li><span><b>${esc(name)}</b><span class="member-id">${esc(person.userId)}</span></span>${action}</li>`;
}

async function addMember(form) {
  await api("POST", "/api/members", {
    userId: form.querySelector("[name=userId]").value,
    displayName: form.querySelector("[name=displayName]").value,
  });
  toast("已加入");
  renderMembers();
}

async function removeMember(userId) {
  if (!confirm("從名單移除後，這個人就不能再進來。")) return;
  await api("DELETE", "/api/members/" + encodeURIComponent(userId));
  toast("已移除");
  renderMembers();
}

function renderProducts() {
  const categories = state.categories || [];
  app.innerHTML = categories.length
    ? `<p class="page-lead">同一個類型共用這組話術。句子裡的 {產品} 會換成當時選到的名稱。</p>${categories.map(categoryCard).join("")}`
    : emptyState("types");
  app.querySelectorAll("form[data-category]").forEach((form) => {
    form.onsubmit = (event) => {
      event.preventDefault();
      guard(() => saveCategory(form));
    };
  });
  app.querySelectorAll("[data-add-product]").forEach((form) => {
    form.onsubmit = (event) => {
      event.preventDefault();
      guard(() => addProduct(form));
    };
  });
  app.querySelectorAll("[data-remove-product]").forEach((btn) => {
    btn.onclick = () => guard(() => removeProduct(btn.dataset.removeProduct));
  });
}

function categoryCard(category) {
  const lines = category.lines || [];
  const count = Math.min(20, Math.max(lines.length + 1, 10));
  const boxes = [];
  for (let i = 0; i < count; i += 1) {
    boxes.push(`<div class="line-row"><span class="line-no">${i + 1}</span><textarea class="line" name="line" placeholder="再加一句">${esc(lines[i] || "")}</textarea></div>`);
  }
  const products = category.products || [];
  return `
    <details class="card">
      <summary>${esc(category.name)} · ${products.length} 項 · ${lines.length} 句</summary>
      <form data-category="${category.id}">
        <label>類型名稱</label>
        <input type="text" name="name" value="${esc(category.name)}" required>
        <p class="muted">空行不會存。</p>
        ${boxes.join("")}
        <div class="actions"><button type="submit" class="wide">儲存話術</button></div>
      </form>
      <ul class="product-list">
        ${products.map((item) => `<li><span>${esc(item.name)}</span><button type="button" class="text-danger" data-remove-product="${item.id}">刪除</button></li>`).join("")}
      </ul>
      <form data-add-product="${category.id}">
        <label>在這個類型加產品</label>
        <input type="text" name="name" placeholder="產品名稱" required>
        <div class="actions"><button type="submit">加入產品</button></div>
      </form>
    </details>`;
}

async function saveCategory(form) {
  const lines = [...form.querySelectorAll("[name=line]")].map((el) => el.value);
  await api("PUT", "/api/categories/" + form.dataset.category, {
    name: form.querySelector("[name=name]").value,
    lines,
  });
  toast("已儲存");
  await reload();
  render();
}

async function addProduct(form) {
  await api("POST", "/api/products", {
    categoryId: Number(form.dataset.addProduct),
    name: form.querySelector("[name=name]").value,
    enabled: true,
  });
  toast("已加入");
  await reload();
  render();
}

async function removeProduct(id) {
  if (!confirm("刪掉這個產品？已經交出去的任務仍會留下當時的名稱。")) return;
  await api("DELETE", "/api/products/" + id);
  toast("已刪除");
  await reload();
  render();
}

function openPost(url) {
  if (!/^https?:\/\/([a-z0-9-]+\.)*threads\.(com|net)\//i.test(url)) return;
  if (window.liff && liff.openWindow) liff.openWindow({ url, external: true });
  else window.open(url, "_blank");
}

async function writeClipboard(text) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const el = document.createElement("textarea");
    el.value = text;
    document.body.appendChild(el);
    el.select();
    document.execCommand("copy");
    el.remove();
  }
}

async function guard(fn) {
  const el = document.activeElement;
  const lock = el && (el.tagName === "BUTTON" || el.tagName === "SELECT") && !el.disabled ? el : null;
  if (lock) lock.disabled = true;
  try {
    await fn();
  } catch (err) {
    if (err instanceof Unauthorized) {
      showLogin();
      return;
    }
    if (err instanceof Forbidden) {
      showForbidden(err.data);
      return;
    }
    toast(err.message || "失敗");
  } finally {
    if (lock && lock.isConnected) lock.disabled = false;
  }
}

function showLogin() {
  const account = document.querySelector("#account");
  const busy = document.querySelector("#busy");
  if (account) account.hidden = true;
  if (busy) busy.hidden = true;
  lockPageScroll(false);
  resetChrome();
  document.querySelector("#tabs").hidden = true;
  document.querySelector("#who").textContent = "";
  app.innerHTML = `
    <section class="panel gate">
      <p class="eyebrow">登入</p>
      <h2>需要重新登入</h2>
      <p class="lead">這次的 LINE 登入已經失效。再用 LINE 登入一次即可。</p>
      <button type="button" class="wide" id="relogin">重新登入</button>
    </section>`;
  app.setAttribute("aria-busy", "false");
  document.querySelector("#relogin").onclick = () => relogin();
}

function showForbidden(data) {
  lockPageScroll(false);
  resetChrome();
  document.querySelector("#tabs").hidden = true;
  document.querySelector("#who").textContent = "";
  app.innerHTML = `
    <section class="panel gate">
      <p class="eyebrow">尚未加入</p>
      <h2>這個帳號還沒加入</h2>
      <p class="lead">${esc(data.displayName || "這組 LINE 帳號還不在名單裡。")}</p>
      <p class="user-id" id="uid">${esc(data.userId || "")}</p>
      <p class="muted">把上面的 userId 貼給管理者，請對方在「成員」加入。管理者本人不用加，那是伺服器上的 LINE_ADMIN_USER_IDS。</p>
      <div class="actions"><button type="button" id="copy-id">複製 userId</button></div>
    </section>`;
  app.setAttribute("aria-busy", "false");
  document.querySelector("#copy-id").onclick = () => guard(async () => {
    await writeClipboard(data.userId || "");
    toast("已複製");
  });
}

function toast(message) {
  const el = document.querySelector("#toast");
  el.hidden = false;
  el.textContent = message;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => {
    el.hidden = true;
  }, 2400);
}

function shortTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const month = date.getMonth() + 1;
  const day = date.getDate();
  const hour = String(date.getHours()).padStart(2, "0");
  const minute = String(date.getMinutes()).padStart(2, "0");
  return `${month}/${day} ${hour}:${minute}`;
}

bindAccountPicker();

function bindAccountPicker() {
  const dialog = document.querySelector("#account");
  const closeAccount = () => {
    state.pendingTaskId = "";
    dialog.hidden = true;
    lockPageScroll(false);
  };
  dialog.addEventListener("click", (event) => {
    if (event.target !== dialog) return;
    closeAccount();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    if (!dialog.hidden) {
      closeAccount();
      return;
    }
    if (state.tab === "week" && state.openTaskId) slideTaskClosed();
  });
  document.querySelectorAll("#account [data-account]").forEach((btn) => {
    btn.onclick = () => {
      const taskId = state.pendingTaskId;
      const account = btn.dataset.account;
      state.pendingTaskId = "";
      dialog.hidden = true;
      lockPageScroll(false);
      const input = app.querySelector(`[data-file="${taskId}"]`);
      if (!input) return;
      input.dataset.account = account;
      input.click();
    };
  });
  document.querySelector("#account-cancel").onclick = () => closeAccount();
}

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[ch]));
}
