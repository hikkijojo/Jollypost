const state = {
  me: null,
  products: [],
  categories: [],
  week: null,
  weekOffset: 0,
  tab: "inbox",
};

const proofBlobs = new Map();

class Forbidden extends Error {
  constructor(data) {
    super("forbidden");
    this.data = data;
  }
}

const app = document.querySelector("#app");

boot().catch((err) => {
  if (err instanceof Forbidden) {
    showForbidden(err.data);
    return;
  }
  app.textContent = err.message || "打不開";
});

async function boot() {
  const liffId = document.body.dataset.liffId || "";
  if (!liffId || !window.liff) {
    throw new Error("LIFF 還沒設定好");
  }
  await liff.init({ liffId });
  if (!liff.isLoggedIn()) {
    liff.login({ redirectUri: location.href });
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
  if (!value) {
    liff.login({ redirectUri: location.href });
    throw new Error("正在重新登入");
  }
  return value;
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
  if (res.status === 403 && data.userId) throw new Forbidden(data);
  if (!res.ok) throw new Error(data.error?.message || "請求失敗");
  return data.data;
}

function render() {
  document.querySelector("#tabs").hidden = false;
  document.querySelector('[data-tab="products"]').hidden = !state.me.admin;
  document.querySelector("#who").textContent = state.me.displayName || "";
  document.querySelectorAll("#tabs button").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.tab === state.tab);
  });
  if (state.tab === "inbox") renderInbox();
  if (state.tab === "week") renderWeek();
  if (state.tab === "products") renderProducts();
}

function renderInbox() {
  app.innerHTML = `
    <section class="panel">
      <h2>貼上老闆的訊息</h2>
      <p class="hint">10/11-3
https://www.threads.com/share/xxxx/

或是

https://www.threads.com/share/xxxx/
Jolly 床邊床</p>
      <label for="paste">訊息</label>
      <textarea id="paste" placeholder="整段貼上"></textarea>
      <div class="actions">
        <button type="button" id="import">寫入本週任務</button>
      </div>
    </section>`;
  document.querySelector("#import").onclick = () => guard(importTasks);
}

async function importTasks() {
  const text = document.querySelector("#paste").value;
  const result = await api("POST", "/api/tasks/import", { text });
  const dup = (result.tasks || []).some((task) => task.duplicate);
  toast(dup ? `已寫入 ${result.tasks.length} 則，其中有連結本週重複` : `已寫入 ${result.tasks.length} 則`);
  state.weekOffset = 0;
  state.tab = "week";
  await reload();
  render();
}

function renderWeek() {
  const week = state.week;
  const tasks = week.tasks || [];
  app.innerHTML = `
    <section class="panel">
      <div class="row">
        <button type="button" class="ghost" id="prev">上一週</button>
        <strong>${esc(week.label)}</strong>
        <button type="button" class="ghost" id="next" ${week.week >= 0 ? "disabled" : ""}>下一週</button>
      </div>
      <p>共 ${week.total} 則，已交圖 ${week.submitted} 則</p>
      <ul class="stats">${(week.byUser || []).map((person) => `<li>${esc(person.name)}　已交圖 ${person.submitted} / ${person.total}</li>`).join("")}</ul>
    </section>
    ${tasks.length ? tasks.map(taskCard).join("") : `<p class="muted">這週還沒有任務。</p>`}`;
  document.querySelector("#prev").onclick = () => guard(() => moveWeek(-1));
  document.querySelector("#next").onclick = () => guard(() => moveWeek(1));
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
  app.querySelectorAll("[data-upload]").forEach((input) => {
    input.onchange = () => {
      const file = input.files && input.files[0];
      input.value = "";
      if (file) guard(() => uploadProof(input.dataset.upload, file));
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
}

function taskCard(task) {
  const product = state.products.find((item) => item.id === task.productId);
  const lines = product ? product.lines.map((line) => applyProductName(line, product.name)) : [];
  const status = { pending: "待回", copied: "已複製", submitted: "已交圖" }[task.status] || task.status;
  return `
    <article class="card">
      <p class="code">${esc(task.code)}</p>
      <p>
        <span class="badge ${task.status === "submitted" ? "ok" : ""}">${esc(status)}</span>
        ${task.duplicate ? `<span class="badge warn">本週已有這則連結</span>` : ""}
        ${task.unmatched ? `<span class="badge warn">清單沒有「${esc(task.productText)}」</span>` : ""}
      </p>
      <p class="muted">${esc(task.createdByName)} · ${esc(shortTime(task.createdAt))}${product ? " · " + esc(product.categoryName) : ""}</p>
      <div class="actions">
        <button type="button" class="ghost" data-open="${esc(task.url)}">打開貼文</button>
      </div>
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
      ${lines.length ? lines.map((line) => `<button type="button" class="reply" data-copy="${task.id}" data-text="${esc(line)}">${esc(line)}</button>`).join("") : `<p class="muted">先選擇產品，才看得到這個類型的話術。</p>`}
      <div class="actions">
        <label class="file">上傳截圖押編號<input data-upload="${task.id}" type="file" accept="image/*" hidden></label>
        <button type="button" class="danger" data-delete="${task.id}">刪除</button>
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

async function moveWeek(step) {
  state.weekOffset += step;
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

async function uploadProof(taskId, file) {
  showBusy("正在押上編號…");
  try {
    const body = new FormData();
    body.append("file", file);
    const res = await fetch("/api/tasks/" + taskId + "/proof", {
      method: "POST",
      headers: { Authorization: "Bearer " + (await token()) },
      body,
    });
    const data = await res.json().catch(() => ({}));
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
  if (!confirm("刪掉後，這個編號不會再拿去用。")) return;
  await api("DELETE", "/api/tasks/" + taskId);
  toast("已刪除");
  state.week = await api("GET", "/api/tasks?week=" + state.weekOffset);
  render();
}

async function loadProof(img) {
  const res = await fetch(img.dataset.proof, {
    headers: { Authorization: "Bearer " + (await token()) },
  });
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
}

function hideBusy() {
  document.querySelector("#busy").hidden = true;
}

function renderProducts() {
  const categories = state.categories || [];
  app.innerHTML = categories.length
    ? categories.map(categoryCard).join("")
    : `<p class="muted">還沒有類型。</p>`;
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
    boxes.push(`<textarea class="line" name="line">${esc(lines[i] || "")}</textarea>`);
  }
  const products = category.products || [];
  return `
    <details class="card">
      <summary>${esc(category.name)} · ${products.length} 項 · ${lines.length} 句</summary>
      <form data-category="${category.id}">
        <label>類型名稱</label>
        <input type="text" name="name" value="${esc(category.name)}" required>
        <p class="muted">話術裡的 {產品} 會換成當時選到的產品名稱。空行不會存。</p>
        ${boxes.join("")}
        <div class="actions"><button type="submit">儲存話術</button></div>
      </form>
      <ul class="stats">
        ${products.map((item) => `<li>${esc(item.name)} <button type="button" class="danger" data-remove-product="${item.id}">刪除</button></li>`).join("")}
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
  try {
    await fn();
  } catch (err) {
    if (err instanceof Forbidden) {
      showForbidden(err.data);
      return;
    }
    toast(err.message || "失敗");
  }
}

function showForbidden(data) {
  document.querySelector("#tabs").hidden = true;
  document.querySelector("#who").textContent = "";
  app.innerHTML = `
    <section class="panel">
      <h2>這個帳號還沒加入</h2>
      <p>${esc(data.displayName || "")}</p>
      <p class="user-id" id="uid">${esc(data.userId || "")}</p>
      <p>把上面的 userId 加進 <code>.env</code> 的 <code>LINE_ALLOWED_USER_IDS</code>。若也要改話術，同時加進 <code>LINE_ADMIN_USER_IDS</code>。存檔後重新整理。</p>
      <button type="button" id="copy-id">複製 userId</button>
    </section>`;
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

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[ch]));
}
