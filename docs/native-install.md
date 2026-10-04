# Debian / Ubuntu 原生一键安装

适用于使用 systemd 的 Debian / Ubuntu VPS，支持 x86_64（amd64）和 aarch64（arm64），不需要 Docker。安装器版本：**1.2.0**。

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

安装器自动补齐依赖，在下载程序前生成 `scn` 管理命令。优先下载本仓库的 Release；没有可用发布包时尝试 `Earl9/subconv-next` 上游 Release。下载后必须通过 `checksums.txt` 的 SHA-256 校验及程序版本检查。发布包不可用时，自动下载本仓库 `main` 分支源码，安装符合 `go.mod` 要求的 Go 并编译。源码编译需要能访问 GitHub、Go 下载站和 Go 模块源；首次编译可能需要几分钟。

服务以独立用户运行，启用 systemd 开机自启。只有服务运行且 `/healthz` 返回成功，安装器才报告安装成功；否则返回非零退出码。

## 管理与访问

安装后输入：

```bash
scn
```

`scn` 是安装器生成的程序，不需要另装“命令执行工具”。下载中断后也可以使用 `scn install` 重试。若安装器尚未开始运行、管理器也未生成，重新执行上面的安装命令。

默认监听 `127.0.0.1:9876`，远程电脑不能直接用服务器 IP 访问。已有 Caddy 可将域名反代到 `127.0.0.1:9876`，再执行 `scn url https://你的域名`。需用服务器 IP 和端口访问时执行：

```bash
scn bind public
scn token
```

在服务器安全组/防火墙放行 TCP `9876` 后，打开 `http://服务器IP:9876/`，使用 `scn token` 显示的 Token 登录。切回本机监听：`scn bind local`。配置公网基础地址用于生成订阅链接，不会自动修改 DNS 或签发 HTTPS 证书。

| 命令 | 功能 |
| --- | --- |
| `scn status` | 服务状态与监听地址 |
| `scn start` / `scn stop` / `scn restart` | 启动、停止、重启 |
| `scn logs` | 查看最近日志并持续跟随，Ctrl+C 返回 |
| `scn token` / `scn reset-token` | 查看或重置登录 Token |
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
| `/etc/subconv-next/subconv-next.env` | 监听设置、Token、数据目录等 |
| `/etc/subconv-next/config.json` | 初始配置；程序自动补齐默认值 |
| `/var/lib/subconv-next` | 工作区、订阅、缓存等持久化数据 |
| `/etc/systemd/system/subconv-next.service` | systemd 服务 |
| `/opt/subconv-next/go` | apt 的 Go 版本不足时使用的独立工具链 |

重复安装和更新会保留已有 Token、配置与数据，并实际重启服务。替换二进制或修改设置后，如果启动或健康检查失败，自动恢复原来的程序、环境文件、初始配置、unit 及此前的服务启用/运行状态。恢复过程不删除或回退运行时数据；重大版本升级前建议另做数据备份。

`scn uninstall` 需要输入 `UNINSTALL` 确认。它保留配置、数据、系统用户及 Go 工具链，再次安装可以继续使用。

## 排错

- **`scn: command not found`**：重新执行一键安装命令；安装器会重建管理命令。如果脚本已执行成功，也可输入 `/usr/local/bin/scn`。
- **GitHub 下载失败**：安装器会尝试其它发布来源和源码编译；所有下载失败时会显示错误并退出，稍后运行 `scn install`。
- **端口占用**：首次安装默认使用 `9876`，不会停止其它服务。先释放此端口再安装；已安装后可通过 `scn port 新端口` 调整。
- **服务启动失败**：执行 `scn logs` 查看原因，再运行 `scn repair`。
- **网页打不开**：检查 `scn status` 的监听地址；默认仅本机监听。如已公网监听，再检查服务器安全组/防火墙是否放行对应端口。
- **登录约 12 小时后需要重新登录**：这是程序的管理会话有效期，不是缺少依赖，也不影响后台 systemd 服务持续运行。

## 开发验证

回归测试只在临时目录中操作，模拟 systemd，不修改本机服务：

```bash
bash -n subconv-next-onekey.sh
python3 scripts/test-native-install.py
```

覆盖首次安装、流式执行下载失败后管理器仍可用、菜单重复打开、更新保留配置及数据、失败恢复、端口校验和环境文件不被当作 Shell 执行。
