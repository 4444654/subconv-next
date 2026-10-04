const form = document.getElementById("login-form");
const usernameInput = document.getElementById("username");
const passwordInput = document.getElementById("password");
const submitButton = document.getElementById("login-submit");
const toggleButton = document.getElementById("toggle-password");
const message = document.getElementById("login-message");
const confirmationInput = document.getElementById("confirm-password");
const confirmationField = document.getElementById("confirm-password-field");
const registerHelp = document.getElementById("register-help");
const loginModeButton = document.getElementById("login-mode");
const registerModeButton = document.getElementById("register-mode");
let registrationEnabled = false;
let registerMode = false;
let formReady = false;

document.addEventListener("DOMContentLoaded", initializeLogin);
form.addEventListener("submit", handleLogin);
toggleButton.addEventListener("click", togglePasswordVisibility);
loginModeButton.addEventListener("click", () => setAuthMode(false));
registerModeButton.addEventListener("click", () => setAuthMode(true));

async function initializeLogin() {
  try {
    const response = await fetch("/api/auth/session", {
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
    const session = await response.json();
    if (!response.ok) throw new Error("session unavailable");
    if (session.authenticated) {
      window.location.replace(safeNextPath());
      return;
    }
    if (!session.configured) {
      setMessage("服务尚未配置管理账号，请联系管理员。", true);
      return;
    }
    registrationEnabled = Boolean(session.registration_enabled);
    registerModeButton.hidden = !registrationEnabled;
    formReady = true;
    setAuthMode(registrationEnabled && new URLSearchParams(window.location.search).get("mode") === "register");
    if (usernameInput.value.trim()) passwordInput.focus();
    else usernameInput.focus();
  } catch (_error) {
    setMessage("无法连接管理服务，请稍后重试。", true);
  }
}

function setAuthMode(register) {
  registerMode = Boolean(register && registrationEnabled);
  document.getElementById("login-title").textContent = registerMode ? "创建你的账号" : "登录 SubConv Next";
  document.getElementById("login-description").textContent = registerMode
    ? "拥有自己的订阅转换工作区。"
    : "登录后开始转换和管理你的订阅。";
  document.title = `${registerMode ? "注册" : "登录"} · SubConv Next`;
  loginModeButton.setAttribute("aria-pressed", String(!registerMode));
  registerModeButton.setAttribute("aria-pressed", String(registerMode));
  confirmationField.hidden = !registerMode;
  confirmationInput.required = registerMode;
  registerHelp.hidden = !registerMode;
  passwordInput.autocomplete = registerMode ? "new-password" : "current-password";
  passwordInput.type = "password";
  confirmationInput.type = "password";
  confirmationInput.value = "";
  passwordInput.value = "";
  toggleButton.classList.remove("password-visible");
  toggleButton.setAttribute("aria-label", "显示密码");
  toggleButton.title = "显示密码";
  if (registerMode && usernameInput.value === "admin") usernameInput.value = "";
  submitButton.querySelector("span").textContent = registerMode ? "注册并登录" : "登录";
  setMessage("");
  setFormEnabled(formReady);
  usernameInput.focus();
}

async function handleLogin(event) {
  event.preventDefault();
  const username = usernameInput.value.trim();
  const password = passwordInput.value;
  if (!username) {
    setMessage("请输入账号。", true);
    usernameInput.focus();
    return;
  }
  if (!password) {
    setMessage("请输入密码。", true);
    passwordInput.focus();
    return;
  }
  if (registerMode) {
    if (!/^[a-zA-Z0-9_.@-]{3,64}$/.test(username)) {
      setMessage("账号须为 3–64 位英文字母、数字或 _ . @ -。", true);
      usernameInput.focus();
      return;
    }
    const passwordBytes = new TextEncoder().encode(password).length;
    if (passwordBytes < 8 || passwordBytes > 72) {
      setMessage("密码长度须为 8–72 字节。", true);
      passwordInput.focus();
      return;
    }
    if (password !== confirmationInput.value) {
      setMessage("两次输入的密码不一致。", true);
      confirmationInput.focus();
      return;
    }
  }

  setBusy(true);
  setMessage("");
  try {
    const body = registerMode
      ? { username, password, confirm_password: confirmationInput.value }
      : { username, password };
    const response = await fetch(registerMode ? "/api/auth/register" : "/api/auth/login", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    });
    const payload = await response.json();
    if (!response.ok || !payload.authenticated) {
      setMessage(authErrorMessage(payload, response.status), true);
      if (payload?.error?.code === "ACCOUNT_EXISTS") usernameInput.focus();
      else passwordInput.select();
      return;
    }
    window.location.replace(safeNextPath());
  } catch (_error) {
    setMessage(`${registerMode ? "注册" : "登录"}请求失败，请检查网络后重试。`, true);
  } finally {
    setBusy(false);
  }
}

function authErrorMessage(payload, status) {
  const code = payload?.error?.code;
  const messages = {
    ACCOUNT_EXISTS: "这个账号已被使用，请换一个账号或直接登录。",
    INVALID_USERNAME: "账号须为 3–64 位英文字母、数字或 _ . @ -。",
    INVALID_PASSWORD: "密码长度须为 8–72 字节。",
    PASSWORD_MISMATCH: "两次输入的密码不一致。",
    REGISTRATION_DISABLED: "当前暂未开放注册，请使用已有账号登录。",
    REGISTRATION_FULL: "注册名额已满，请联系管理员。",
    REGISTRATION_FAILED: "账号保存失败，请稍后重试。",
    AUTH_BUSY: "服务繁忙，请稍后重试。",
    CROSS_ORIGIN_REQUEST: `访问地址与服务配置不一致，请在服务器执行 scn url ${window.location.origin}，再刷新本页重试。`,
  };
  if (messages[code]) return messages[code];
  if (status === 429) return "尝试次数过多，请一分钟后再试。";
  if (status === 401) return "账号或密码不正确。";
  return registerMode ? "注册失败，请稍后重试。" : payload?.error?.message || "登录失败，请稍后重试。";
}

function togglePasswordVisibility() {
  const visible = passwordInput.type === "text";
  passwordInput.type = visible ? "password" : "text";
  confirmationInput.type = passwordInput.type;
  toggleButton.classList.toggle("password-visible", !visible);
  toggleButton.setAttribute("aria-label", visible ? "显示密码" : "隐藏密码");
  toggleButton.title = visible ? "显示密码" : "隐藏密码";
  passwordInput.focus();
}

function safeNextPath() {
  const candidate = new URLSearchParams(window.location.search).get("next") || "/";
  try {
    const target = new URL(candidate, window.location.origin);
    if (target.origin !== window.location.origin || target.pathname.startsWith("/login")) {
      return "/";
    }
    return `${target.pathname}${target.search}${target.hash}`;
  } catch (_error) {
    return "/";
  }
}

function setFormEnabled(enabled) {
  usernameInput.disabled = !enabled;
  passwordInput.disabled = !enabled;
  submitButton.disabled = !enabled;
  toggleButton.disabled = !enabled;
  confirmationInput.disabled = !enabled || !registerMode;
  loginModeButton.disabled = !enabled;
  registerModeButton.disabled = !enabled;
}

function setBusy(busy) {
  setFormEnabled(!busy && formReady);
  submitButton.querySelector("span").textContent = busy
    ? registerMode ? "正在注册" : "正在验证"
    : registerMode ? "注册并登录" : "登录";
}

function setMessage(text, isError = false) {
  message.textContent = text;
  message.classList.toggle("visible", Boolean(text));
  message.classList.toggle("error", Boolean(text) && isError);
}
