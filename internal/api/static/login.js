const form = document.getElementById("login-form");
const usernameInput = document.getElementById("username");
const passwordInput = document.getElementById("password");
const submitButton = document.getElementById("login-submit");
const toggleButton = document.getElementById("toggle-password");
const message = document.getElementById("login-message");

document.addEventListener("DOMContentLoaded", initializeLogin);
form.addEventListener("submit", handleLogin);
toggleButton.addEventListener("click", togglePasswordVisibility);

async function initializeLogin() {
  try {
    const response = await fetch("/api/auth/session", {
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
    const session = await response.json();
    if (session.authenticated) {
      window.location.replace(safeNextPath());
      return;
    }
    if (!session.configured) {
      setMessage("服务尚未配置管理账号，请联系管理员。", true);
      return;
    }
    setFormEnabled(true);
    if (usernameInput.value.trim()) passwordInput.focus();
    else usernameInput.focus();
  } catch (_error) {
    setMessage("无法连接管理服务，请稍后重试。", true);
  }
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

  setBusy(true);
  setMessage("");
  try {
    const response = await fetch("/api/auth/login", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ username, password }),
    });
    const payload = await response.json();
    if (!response.ok || !payload.authenticated) {
      const text =
        response.status === 429
          ? "尝试次数过多，请一分钟后再试。"
          : response.status === 401
            ? "账号或密码不正确。"
            : payload?.error?.code === "CROSS_ORIGIN_REQUEST"
              ? `访问地址与服务配置不一致，请在服务器执行 scn url ${window.location.origin}，再刷新本页登录。`
              : payload?.error?.message || "登录失败，请稍后重试。";
      setMessage(text, true);
      passwordInput.select();
      return;
    }
    window.location.replace(safeNextPath());
  } catch (_error) {
    setMessage("登录请求失败，请检查网络后重试。", true);
  } finally {
    setBusy(false);
  }
}

function togglePasswordVisibility() {
  const visible = passwordInput.type === "text";
  passwordInput.type = visible ? "password" : "text";
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
}

function setBusy(busy) {
  usernameInput.disabled = busy;
  passwordInput.disabled = busy;
  submitButton.disabled = busy;
  toggleButton.disabled = busy;
  submitButton.querySelector("span").textContent = busy ? "正在验证" : "登录";
}

function setMessage(text, isError = false) {
  message.textContent = text;
  message.classList.toggle("visible", Boolean(text));
  message.classList.toggle("error", Boolean(text) && isError);
}
