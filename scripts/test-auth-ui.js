#!/usr/bin/env node
// Execute auth UI behavior with a small DOM fixture; no browser dependency.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const root = path.resolve(__dirname, "..");
const login = fs.readFileSync(path.join(root, "internal/api/static/login.js"), "utf8");
const app = fs.readFileSync(path.join(root, "internal/api/static/app.js"), "utf8");

function fixture({ open = true, search = "", reply = { status: 201, payload: { authenticated: true } } } = {}) {
  const nodes = {};
  for (const id of ["login-form", "username", "password", "login-submit", "toggle-password", "login-message", "confirm-password", "confirm-password-field", "register-help", "login-mode", "register-mode", "login-title", "login-description"]) {
    const classes = new Set();
    const span = { textContent: "" };
    nodes[id] = {
      value: id === "username" ? "admin" : "", type: "password", disabled: true,
      hidden: false, textContent: "", attributes: {}, events: {},
      classList: { remove: name => classes.delete(name), toggle(name, yes) { if (yes) classes.add(name); else classes.delete(name); } },
      setAttribute(name, value) { this.attributes[name] = value; },
      addEventListener(name, handler) { this.events[name] = handler; },
      querySelector: () => span, focus() {}, select() {},
    };
  }
  const requests = [];
  let destination = "";
  const context = vm.createContext({
    document: { getElementById: id => nodes[id], addEventListener() {}, title: "" },
    window: { location: { search, origin: "https://sub.example.com", replace(value) { destination = value; } } },
    URL, URLSearchParams, TextEncoder,
    fetch: async (url, options) => {
      requests.push({ url, options });
      if (url === "/api/auth/session") return { ok: true, json: async () => ({ ok: true, configured: true, registration_enabled: open }) };
      return { ok: reply.status >= 200 && reply.status < 300, status: reply.status, json: async () => reply.payload };
    },
  });
  vm.runInContext(login, context);
  return { nodes, requests, context, destination: () => destination };
}

async function run() {
  const registered = fixture({ search: "?mode=register&next=%2F%23workspace-yaml" });
  await vm.runInContext("initializeLogin()", registered.context);
  assert.equal(registered.nodes["register-mode"].hidden, false);
  assert.equal(registered.nodes["confirm-password-field"].hidden, false);
  assert.equal(registered.nodes.password.autocomplete, "new-password");
  assert.equal(registered.nodes.username.value, "");
  registered.nodes.username.value = "alice";
  registered.nodes.password.value = "  password123!  ";
  registered.nodes["confirm-password"].value = "mismatched";
  await vm.runInContext("handleLogin({ preventDefault() {} })", registered.context);
  assert.equal(registered.requests.length, 1, "confirmation mismatch must not reach the API");
  assert.match(registered.nodes["login-message"].textContent, /不一致/);
  registered.nodes["confirm-password"].value = registered.nodes.password.value;
  await vm.runInContext("handleLogin({ preventDefault() {} })", registered.context);
  const request = registered.requests[1];
  assert.equal(request.url, "/api/auth/register");
  assert.deepEqual(JSON.parse(request.options.body), { username: "alice", password: "  password123!  ", confirm_password: "  password123!  " });
  assert.equal(registered.destination(), "/#workspace-yaml");
  assert.equal(registered.nodes["login-submit"].disabled, false);

  const duplicate = fixture({ search: "?mode=register", reply: { status: 409, payload: { error: { code: "ACCOUNT_EXISTS" } } } });
  await vm.runInContext("initializeLogin()", duplicate.context);
  duplicate.nodes.username.value = "alice";
  duplicate.nodes.password.value = duplicate.nodes["confirm-password"].value = "password123";
  await vm.runInContext("handleLogin({ preventDefault() {} })", duplicate.context);
  assert.match(duplicate.nodes["login-message"].textContent, /已被使用/);
  assert.equal(duplicate.destination(), "");
  duplicate.nodes.password.value = duplicate.nodes["confirm-password"].value = "密".repeat(25);
  const count = duplicate.requests.length;
  await vm.runInContext("handleLogin({ preventDefault() {} })", duplicate.context);
  assert.equal(duplicate.requests.length, count, "UTF-8 passwords must obey the backend byte limit");

  const closed = fixture({ open: false, search: "?mode=register", reply: { status: 200, payload: { authenticated: true } } });
  await vm.runInContext("initializeLogin()", closed.context);
  assert.equal(closed.nodes["register-mode"].hidden, true);
  assert.equal(closed.nodes["confirm-password-field"].hidden, true);
  assert.equal(closed.nodes.password.autocomplete, "current-password");
  closed.nodes.password.value = "password123";
  await vm.runInContext("handleLogin({ preventDefault() {} })", closed.context);
  assert.equal(closed.requests[1].url, "/api/auth/login");
  assert.deepEqual(JSON.parse(closed.requests[1].options.body), { username: "admin", password: "password123" });

  const originError = fixture({ reply: { status: 403, payload: { error: { code: "CROSS_ORIGIN_REQUEST" } } } });
  await vm.runInContext("initializeLogin()", originError.context);
  originError.nodes.password.value = "password123";
  await vm.runInContext("handleLogin({ preventDefault() {} })", originError.context);
  assert.match(originError.nodes["login-message"].textContent, /scn url https:\/\/sub\.example\.com/);

  const storageFunction = app.match(/function draftStorageKey\(baseKey\) \{[\s\S]*?\n\}/)[0];
  const storageContext = vm.createContext({ state: { role: "user", userId: "u_alice" } });
  vm.runInContext(storageFunction, storageContext);
  const aliceKey = vm.runInContext('draftStorageKey("SUBCONV_LOCAL_DRAFTS")', storageContext);
  storageContext.state.userId = "u_bob";
  const bobKey = vm.runInContext('draftStorageKey("SUBCONV_LOCAL_DRAFTS")', storageContext);
  assert.notEqual(aliceKey, bobKey);
  storageContext.state.role = "admin";
  assert.equal(vm.runInContext('draftStorageKey("SUBCONV_LOCAL_DRAFTS")', storageContext), "SUBCONV_LOCAL_DRAFTS");
  assert.doesNotMatch(app, /localStorage\.(?:getItem|setItem|removeItem)\(LOCAL_DRAFT/);

  const authFunction = app.match(/async function loadAuthSession\(\) \{[\s\S]*?\n\}/)[0];
  const unavailable = vm.createContext({ fetch: async () => ({ ok: false, json: async () => ({}) }), showToast() {}, state: {} });
  vm.runInContext(authFunction, unavailable);
  assert.equal(await vm.runInContext("loadAuthSession()", unavailable), false, "an unavailable session must not load shared drafts");
  const startup = app.match(/document.addEventListener\("DOMContentLoaded", \(\) => \{[\s\S]*?\n\}\);/)[0];
  const startupCalls = [];
  const startupContext = vm.createContext({
    document: { addEventListener(_name, callback) { callback(); } },
    decorateStaticIcons() {}, decorateButtons() {}, renderOutputTiles() {},
    applyDefaultState() { startupCalls.push("drafts"); },
    bindEvents() { startupCalls.push("draft-actions"); },
    init() { startupCalls.push("auth-init"); },
  });
  vm.runInContext(startup, startupContext);
  assert.deepEqual(startupCalls, ["auth-init"], "shared drafts must not render or accept actions before authentication");

  const logoutFunction = app.match(/async function logoutManagementSession\(\) \{[\s\S]*?\n\}/)[0];
  for (const scenario of [
    { name: "success", status: 200, payload: { ok: true }, redirect: true },
    { name: "expired session", status: 401, redirect: true },
    { name: "CSRF failure", status: 403, redirect: false },
    { name: "server failure", status: 500, redirect: false },
    { name: "failed response body", status: 200, payload: { ok: false }, redirect: false },
    { name: "network failure", networkError: true, redirect: false },
  ]) {
    const button = { disabled: false };
    const messages = [];
    let destination = "";
    const context = vm.createContext({
      state: { csrfToken: "logout-csrf" },
      document: { getElementById: () => button },
      window: { location: { replace(value) { destination = value; } } },
      showToast(message) { messages.push(message); },
      fetch: async (url, options) => {
        assert.equal(url, "/api/auth/logout");
        assert.equal(options.method, "POST");
        assert.equal(options.credentials, "same-origin");
        assert.equal(options.headers["X-SubConv-CSRF"], "logout-csrf");
        if (scenario.networkError) throw new Error("offline");
        return { ok: scenario.status === 200, status: scenario.status,
          json: async () => scenario.payload || { error: { code: "FAILED" } } };
      },
    });
    vm.runInContext(logoutFunction, context);
    await vm.runInContext("logoutManagementSession()", context);
    assert.equal(destination, scenario.redirect ? "/login" : "", scenario.name);
    assert.equal(messages.length, scenario.redirect ? 0 : 1, scenario.name);
    assert.equal(button.disabled, false, "logout must remain usable after a failed request");
  }
  process.stdout.write("Auth UI checks passed: signup, login, validation, closed registration, origin errors, account draft isolation, failed session loading and logout failures.\n");
}

run().catch(error => { process.stderr.write(`${error.stack}\n`); process.exitCode = 1; });
