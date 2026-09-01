const form = document.getElementById("login-form");
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
      setMessage("服务尚未配置管理密码，当前公网访问已被阻止。", true);
      return;
    }
    setFormEnabled(true);
    passwordInput.focus();
  } catch (_error) {
    setMessage("无法连接管理服务，请稍后重试。", true);
  }
}

async function handleLogin(event) {
  event.preventDefault();
  const password = passwordInput.value.trim();
  if (!password) {
    setMessage("请输入管理密码。", true);
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
      body: JSON.stringify({ password }),
    });
    const payload = await response.json();
    if (!response.ok || !payload.authenticated) {
      const text =
        response.status === 429
          ? "尝试次数过多，请一分钟后再试。"
          : response.status === 401
            ? "管理密码不正确。"
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
  passwordInput.disabled = !enabled;
  submitButton.disabled = !enabled;
  toggleButton.disabled = !enabled;
}

function setBusy(busy) {
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
