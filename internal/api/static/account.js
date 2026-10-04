const accountUI = { users: [], targetUser: null };

function initializeAccountControls() {
  document.getElementById("change-password-btn").addEventListener("click", () => {
    document.getElementById("password-form").reset();
    setAccountMessage("password-message", "");
    document.getElementById("password-dialog").showModal();
    document.getElementById("current-password").focus();
  });
  document.getElementById("users-btn").addEventListener("click", () => {
    document.getElementById("users-dialog").showModal();
    void loadManagedUsers();
  });
  document.getElementById("refresh-users-btn").addEventListener("click", loadManagedUsers);
  document.getElementById("password-form").addEventListener("submit", changeOwnPassword);
  document.getElementById("reset-user-form").addEventListener("submit", resetManagedUserPassword);
  document.getElementById("users-list").addEventListener("click", handleManagedUserAction);
  for (const [dialogID, formID, messageID] of [
    ["password-dialog", "password-form", "password-message"],
    ["reset-user-dialog", "reset-user-form", "reset-user-message"],
  ]) {
    document.getElementById(dialogID).addEventListener("close", () => {
      document.getElementById(formID).reset();
      setAccountMessage(messageID, "");
      if (dialogID === "reset-user-dialog") accountUI.targetUser = null;
    });
  }
}

function updateAccountControls(session) {
  const loggedIn = Boolean(session.required && session.authenticated);
  document.getElementById("change-password-btn")?.classList.toggle("hidden", !loggedIn || session.role !== "user");
  document.getElementById("users-btn")?.classList.toggle("hidden", !loggedIn || session.role !== "admin");
}

function setAccountMessage(id, text) {
  const node = document.getElementById(id);
  node.textContent = text;
  node.hidden = !text;
}

function setAccountFormBusy(id, busy) {
  document.getElementById(id).querySelectorAll("input, button").forEach(node => { node.disabled = busy; });
}

function newPasswordError(password, confirmation) {
  const bytes = new TextEncoder().encode(password).length;
  if (bytes < 8 || bytes > 72) return "新密码须为 8–72 字节。";
  if (password !== confirmation) return "两次输入的新密码不一致。";
  return "";
}

function accountAPIError(response) {
  const messages = {
    ADMINISTRATOR_SCRIPT_ONLY: "管理员账号和密码只能在脚本菜单第 6 项设置。",
    CURRENT_PASSWORD_INVALID: "当前密码不正确。",
    INVALID_PASSWORD: "新密码须为 8–72 字节。",
    PASSWORD_MISMATCH: "两次输入的新密码不一致。",
    CSRF_TOKEN_INVALID: "登录状态已变化，请刷新页面后重试。",
    ACCOUNT_CHANGED: "账号状态已变化，请刷新页面后重试。",
    ACCOUNT_NOT_FOUND: "账号不存在，请刷新用户列表。",
    ACCOUNT_SAVE_FAILED: "账号保存失败，请稍后重试。",
    AUTH_BUSY: "服务繁忙，请稍后重试。",
    FORBIDDEN: "此操作需要管理员权限。",
    RATE_LIMITED: "操作过于频繁，请稍后重试。",
    NETWORK_ERROR: "无法连接服务，请检查网络后重试。",
  };
  return messages[response?.error?.code] || "操作失败，请稍后重试。";
}

async function changeOwnPassword(event) {
  event.preventDefault();
  const current = document.getElementById("current-password").value;
  const next = document.getElementById("new-password").value;
  const confirmation = document.getElementById("new-password-confirm").value;
  const error = !current ? "请输入当前密码。" : newPasswordError(next, confirmation);
  if (error) { setAccountMessage("password-message", error); return; }
  setAccountFormBusy("password-form", true);
  setAccountMessage("password-message", "");
  try {
    const response = await fetchJSON("/api/auth/password", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ current_password: current, new_password: next, confirm_password: confirmation }),
    });
    if (!response?.ok || !response.authenticated) { setAccountMessage("password-message", accountAPIError(response)); return; }
    state.csrfToken = response.csrf_token || "";
    document.getElementById("password-form").reset();
    document.getElementById("password-dialog").close();
    showToast("密码已修改，其他设备的旧登录会话已失效。");
  } finally { setAccountFormBusy("password-form", false); }
}

async function loadManagedUsers() {
  const button = document.getElementById("refresh-users-btn");
  const list = document.getElementById("users-list");
  button.disabled = true;
  list.textContent = "正在加载用户……";
  try {
    const response = await fetchJSON("/api/users");
    if (!response?.ok) { list.textContent = accountAPIError(response); return; }
    accountUI.users = Array.isArray(response.users) ? response.users : [];
    document.getElementById("users-count").textContent = `${accountUI.users.length} / ${response.limit || 256} 个注册用户`;
    renderManagedUsers();
  } finally { button.disabled = false; }
}

function renderManagedUsers() {
  const list = document.getElementById("users-list");
  if (!accountUI.users.length) { list.textContent = "还没有注册用户。"; return; }
  list.innerHTML = accountUI.users.map(user => {
    const created = new Date(user.created_at);
    const time = Number.isNaN(created.getTime()) ? "未知" : created.toLocaleString("zh-CN");
    return `<div class="managed-user-row">
      <div class="managed-user-info"><strong>${escapeHtml(user.username)}</strong><span>注册于 ${escapeHtml(time)}</span></div>
      <span class="managed-user-status ${user.disabled ? "disabled" : ""}">${user.disabled ? "已停用" : "正常"}</span>
      <div class="managed-user-actions">
        <button type="button" class="${user.disabled ? "secondary-button" : "ghost-button"} small-button" data-user-action="toggle" data-user-id="${escapeHtml(user.id)}">${user.disabled ? "启用" : "停用"}</button>
        <button type="button" class="ghost-button small-button" data-user-action="password" data-user-id="${escapeHtml(user.id)}">重置密码</button>
      </div>
    </div>`;
  }).join("");
}

async function handleManagedUserAction(event) {
  const button = event.target.closest("[data-user-action]");
  if (!button) return;
  const user = accountUI.users.find(item => item.id === button.dataset.userId);
  if (!user) return;
  if (button.dataset.userAction === "password") {
    accountUI.targetUser = user;
    document.getElementById("reset-user-title").textContent = `重置 ${user.username} 的密码`;
    document.getElementById("reset-user-form").reset();
    setAccountMessage("reset-user-message", "");
    document.getElementById("reset-user-dialog").showModal();
    document.getElementById("reset-user-password").focus();
    return;
  }
  button.disabled = true;
  try {
    const response = await fetchJSON(`/api/users/${encodeURIComponent(user.id)}`, {
      method: "PATCH", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ disabled: !user.disabled }),
    });
    if (!response?.ok) { showToast(accountAPIError(response), true); return; }
    showToast(user.disabled ? "账号已启用，请用户重新登录。" : "账号已停用，原有登录会话已失效。");
    await loadManagedUsers();
  } finally { button.disabled = false; }
}

async function resetManagedUserPassword(event) {
  event.preventDefault();
  if (!accountUI.targetUser) return;
  const next = document.getElementById("reset-user-password").value;
  const confirmation = document.getElementById("reset-user-confirm").value;
  const error = newPasswordError(next, confirmation);
  if (error) { setAccountMessage("reset-user-message", error); return; }
  setAccountFormBusy("reset-user-form", true);
  setAccountMessage("reset-user-message", "");
  try {
    const response = await fetchJSON(`/api/users/${encodeURIComponent(accountUI.targetUser.id)}/password`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ new_password: next, confirm_password: confirmation }),
    });
    if (!response?.ok) { setAccountMessage("reset-user-message", accountAPIError(response)); return; }
    document.getElementById("reset-user-form").reset();
    document.getElementById("reset-user-dialog").close();
    showToast("密码已重置，该用户需使用新密码重新登录。");
  } finally { setAccountFormBusy("reset-user-form", false); }
}
