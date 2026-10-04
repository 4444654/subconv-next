# Debian / Ubuntu 原生一键安装

适用于使用 systemd 的 Debian / Ubuntu VPS，支持 x86_64（amd64）和 aarch64（arm64），不需要 Docker。安装器版本：**1.4.0**。

## 安装

在服务器的 SSH 终端中使用 root 执行。若提示 `curl: command not found`，先执行：

```bash
apt-get update && apt-get install -y curl ca-certificates
```

一键安装：

```bash
curl -fL --retry 2 https://raw.githubusercontent.com/4444654/subconv-next/main/subconv-next-onekey.sh -o /tmp/subconv-next-onekey.sh && bash /tmp/subconv-next-onekey.sh
```

也支持流式执行：

```bash
bash <(curl -fLsS https://raw.githubusercontent.com/4444654/subconv-next/main/subconv-next-onekey.sh)
```

推荐使用先下载再执行的第一种方式：下载失败时不会运行空脚本。使用普通用户时，将执行脚本的 `bash` 改为 `sudo bash`。

安装器自动补齐依赖，在下载程序前生成 `scn` 管理命令。优先下载本仓库的 Release；没有可用发布包时尝试 `Earl9/subconv-next` 上游 Release。下载后必须通过 `checksums.txt` 的 SHA-256 校验、程序版本检查及账号登录及注册功能检查。发布包不可用或不支持账号登录和注册时，自动下载本仓库 `main` 分支源码，安装符合 `go.mod` 要求的 Go 并编译。源码编译需要能访问 GitHub、Go 下载站和 Go 模块源；首次编译可能需要几分钟。

服务以独立用户运行，启用 systemd 开机自启。只有服务运行且 `/healthz` 返回成功，安装器才报告安装成功；否则返回非零退出码。

## 管理与访问

安装后输入：

```bash
scn
```

`scn` 是安装器生成的程序，不需要另装“命令执行工具”。下载中断后也可以使用 `scn install` 重试。若安装器尚未开始运行、管理器也未生成，重新执行上面的安装命令。

菜单按“服务管理、账号管理、访问设置、安装维护、注册管理”分组，每个选项单独一行，编号 1–15，输入 0 退出。仅刷新已有安装的菜单时，下载当前脚本并运行 `menu-update`：

```bash
curl -fL --retry 2 https://raw.githubusercontent.com/4444654/subconv-next/main/subconv-next-onekey.sh -o /tmp/subconv-next-onekey.sh && bash /tmp/subconv-next-onekey.sh menu-update
```

此命令保存最新管理器；再次输入 `scn` 打开新版菜单。

默认监听 `127.0.0.1:9876`，远程电脑不能直接用服务器 IP 访问。已有 Caddy 可将域名反代到 `127.0.0.1:9876`，再执行 `scn url https://你的域名`。需用服务器 IP 和端口访问时执行：

```bash
scn bind public
scn token
```

在服务器安全组/防火墙放行 TCP `9876` 后，打开 `http://服务器IP:9876/`。默认账号是 **`admin`**，初始密码是 `scn token` 显示的 Token。切回本机监听：`scn bind local`。配置访问地址同时用于浏览器 API 的同源校验和生成订阅链接，不会自动修改 DNS 或签发 HTTPS 证书。

### 通过 Caddy / Nginx 反代登录

反代配置好后，在 SSH 命令行执行（替换为浏览器实际使用的域名）：

```bash
scn url https://sub.example.com
```

也可打开 `scn` 菜单，选择 **11** 设置访问地址。填写协议、域名以及非默认端口（如有），不要包含 `/login`、查询参数或片段。设置会重启服务，并保留账号密码、API Token 和数据。之后通过相同地址打开登录页；如果换了域名或改用 IP 和端口访问，需要同步更新此设置。

Caddy 在前端终止 HTTPS、通过 HTTP 连接本机服务时，程序默认不信任代理头。显式配置访问地址后，登录接口会按该 HTTPS 域名校验 Origin，并签发 Secure 会话 Cookie；无需为登录开启全局代理头信任。

### 设置自己的账号和密码

```bash
scn account
```

按提示输入账号和两次新密码。账号限 1–64 位英文字母、数字、`_ . @ -`；密码限 8–72 字节，支持空格和特殊符号，输入时不显示。密码以 bcrypt 哈希保存，明文不会写入配置或显示在命令参数中。设置完成后需要重新登录。忘记密码时也可在 SSH 终端执行此命令重设。

网页登录密码与 API Token 分开管理；设置账号密码保留原 Token、发布订阅链接及数据。`scn token` 继续查看 API Token，设置独立密码后它不能作为网页密码使用。尚未设置独立密码时，`scn reset-token` 也会改变初始登录密码。

从 v1.2.0 升级请重新执行上方一键安装命令，以同时更新程序和 `scn` 管理器。升级后第一次用 `admin` 和原 Token 登录；已设置过的独立账号密码会在后续更新中保留。当前管理会话有效期仍为 12 小时。

### 前端注册

升级到当前程序后，登录页会显示 **注册账号**。用户填写账号、密码、确认密码，注册成功即自动登录。账号为 3–64 位英文字母、数字或 `_ . @ -`，不区分大小写；密码为 8–72 字节，两次输入必须完全一致。管理员账号名不可注册。

注册用户的工作区、发布订阅和浏览器草稿按账号隔离；普通账号不能读取管理员或其他用户的配置，也不能启用私有网络抓取、跳过 TLS 验证或改动服务账号。账号保存在 `/var/lib/subconv-next/accounts.json`，密码为 bcrypt 哈希，文件权限为 `0600`；更新、重启和卸载保留此文件。

默认开放注册（最多 256 个账号）。打开 `scn` 菜单，选择 **15. 开启 / 关闭注册**，再选择 **1** 开启或 **2** 关闭；主菜单同时显示当前注册开关状态。也可用 `scn registration off` 关闭注册入口和接口，已注册账号仍可登录；用 `scn registration on` 重新开放。匿名公开转换模式不显示注册入口。详情见 [注册与用户隔离](registration.md)。

| 命令 | 功能 |
| --- | --- |
| `scn status` | 服务状态与监听地址 |
| `scn start` / `scn stop` / `scn restart` | 启动、停止、重启 |
| `scn logs` | 查看最近日志并持续跟随，Ctrl+C 返回 |
| `scn account` | 设置或重置网页登录账号与独立密码 |
| `scn registration on` / `scn registration off` | 开放或关闭前端注册 |
| `scn token` / `scn reset-token` | 查看或重置 API Token（初始网页登录密码） |
| `scn port 9876` | 修改端口，检查范围和占用 |
| `scn bind local` / `scn bind public` | 切换本机/公网监听 |
| `scn url https://sub.example.com` | 设置公网基础地址 |
| `scn url ""` | 清除公网基础地址 |
| `scn install` / `scn update` | 安装、重试或更新 |
| `scn repair` | 修复 systemd 服务和管理命令 |
| `scn uninstall` | 卸载程序和服务，保留配置与数据 |

## 文件与更新

| 路径 | 用途 |
| --- | --- |
| `/usr/local/bin/subconv-next` | 主程序 |
| `/usr/local/bin/scn` | 管理脚本 |
| `/etc/subconv-next/subconv-next.env` | 监听设置、API Token、登录账号、密码哈希、数据目录等 |
| `/etc/subconv-next/config.json` | 初始配置；程序自动补齐默认值 |
| `/var/lib/subconv-next` | 工作区、订阅、缓存等持久化数据 |
| `/var/lib/subconv-next/accounts.json` | 注册账号和密码哈希 |
| `/etc/systemd/system/subconv-next.service` | systemd 服务 |
| `/opt/subconv-next/go` | apt 的 Go 版本不足时使用的独立工具链 |

重复安装和更新会保留已有账号密码、Token、配置与数据，并实际重启服务。替换二进制或修改设置后，如果启动或健康检查失败，自动恢复原来的程序、环境文件、初始配置、unit 及此前的服务启用/运行状态。恢复过程不删除或回退运行时数据；重大版本升级前建议另做数据备份。

`scn uninstall` 需要输入 `UNINSTALL` 确认。它保留配置、数据、系统用户及 Go 工具链，再次安装可以继续使用。

## 排错

- **`scn: command not found`**：重新执行一键安装命令；安装器会重建管理命令。如果脚本已执行成功，也可输入 `/usr/local/bin/scn`。
- **GitHub 下载失败**：安装器会尝试其它发布来源和源码编译；所有下载失败时会显示错误并退出，稍后运行 `scn install`。
- **端口占用**：首次安装默认使用 `9876`，不会停止其它服务。先释放此端口再安装；已安装后可通过 `scn port 新端口` 调整。
- **服务启动失败**：执行 `scn logs` 查看原因，再运行 `scn repair`。
- **网页打不开**：检查 `scn status` 的监听地址；默认仅本机监听。如已公网监听，再检查服务器安全组/防火墙是否放行对应端口。
- **登录约 12 小时后需要重新登录**：这是程序的管理会话有效期，不是缺少依赖，也不影响后台 systemd 服务持续运行。
- **登录提示 `cross-origin API request rejected`**：服务配置的访问地址与浏览器地址不一致。通过 `scn` 菜单 11 或 `scn url https://实际域名` 设置与浏览器一致的地址（包括非默认端口），再刷新登录页；不需要重置密码。

## 开发验证

回归测试只在临时目录中操作，模拟 systemd，不修改本机服务：

```bash
bash -n subconv-next-onekey.sh
python3 scripts/test-native-install.py
```

覆盖首次安装、流式执行下载失败后管理器仍可用、菜单重复打开、菜单注册开关及状态刷新、更新保留配置及数据、失败恢复、端口校验、环境文件不被当作 Shell 执行、独立账号设置、Token 保留及账号修改失败恢复。
