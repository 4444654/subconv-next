# 11. 安全与可靠性

## 安全边界

`subconv-next` 只做订阅解析和配置生成。

禁止实现：

- 代理服务
- 节点转发
- 端口转发
- 透明代理
- 防火墙规则修改
- DNS 劫持
- 绕过检测
- 扫描内网

## 网络安全

订阅拉取必须：

- 只允许 http/https。
- 禁止 localhost/private/link-local/multicast。
- 限制 body 大小。
- 限制 redirect。
- 限制超时。
- 禁止在日志输出完整 URL。
- 禁止跟随跳转到内网地址。

## 本地服务暴露

默认：

```text
listen_addr=127.0.0.1
allow_lan=0
```

只有用户显式设置 `allow_lan=1` 才允许局域网访问。

即使 allow_lan=1，也不要监听 WAN 接口检测逻辑；只提供警告。

公网匿名转换使用 `SUBCONV_PUBLIC_CONVERTER=true`。普通访客无需登录，每次访问创建高强度随机工作区，所有有状态接口都校验该工作区能力；发布链接的查看、轮换和删除还必须匹配所属工作区。未列入公开转换白名单的管理路由继续由 `SUBCONV_ACCESS_TOKEN` 保护。不要使用 `SUBCONV_ALLOW_INSECURE_PUBLIC=true` 代替公开转换模式。

任何非回环监听都必须设置至少 24 字符的 `SUBCONV_ACCESS_TOKEN`，私网地址和 Docker 网桥来源不会自动绕过认证。管理 API 默认启用请求体上限、跨来源写请求校验、失败认证限流，并分别限制匿名会话创建、高成本转换、普通 API 请求和发布订阅下载。内置限流仅在单个进程内有效，公网入口仍应在反向代理或边缘防火墙配置 TLS、连接数限制和分布式限流。
公开模式会把空闲工作区限制为最多 6 小时，并在发布链接连续 30 天未访问时清理；若反向代理负责传递真实客户端 IP，只能在后端端口不对公网开放时设置 `SUBCONV_TRUST_PROXY_HEADERS=true`。
公开工作区的抓取大小、超时、刷新间隔和代理信任由服务端固定，客户端不能放大这些参数。单个工作区最多 16 个订阅源、32 个手动源、512 KiB 手动内容和 5000 个最终节点；公网模式不允许由服务端抓取远程自定义规则快照，可改用内联规则或运行时规则提供器。刷新按工作区互斥且全局最多并行 4 个任务；站点图标最多检查 8 个候选并共享 10 秒总预算。出站请求仅允许常见 HTTP/HTTPS Web 端口，并在每次 DNS 解析和重定向后重新检查目标地址。

## 日志脱敏

需要脱敏：

- 订阅 URL query
- token
- password
- uuid 可保留前后 4 位
- private-key
- public-key 可保留，但不建议在日志输出

示例：

```text
https://example.com/api/v1/client/subscribe?token=***
```

## 文件写入

- 运行缓存写 `/var/run/subconv-next`。
- 不要频繁写 `/etc`。
- 只有状态变更时才写 `/etc/subconv-next/state.json`。
- 输出 YAML 默认写 `/var/run`，避免频繁写 flash。

## 崩溃恢复

- daemon 启动时如果输出文件不存在，自动生成一次。
- 刷新失败时不覆盖旧 YAML。
- parser 局部失败不影响其他订阅。
- 单个订阅失败不影响其他订阅。

## 资源限制

默认：

```text
max_subscription_bytes = 5 MiB
max_nodes = 5000
max_inline_bytes = 1 MiB
fetch_timeout = 15s
render_timeout = 10s
```

如果超过限制，返回明确错误。

## 并发控制

刷新操作必须有锁。

如果已有刷新运行，第二个 `/api/refresh` 返回：

```json
{
  "ok": false,
  "error": {
    "code": "REFRESH_IN_PROGRESS",
    "message": "refresh is already running"
  }
}
```

## 配置校验

daemon 启动时校验：

- 端口范围 1–65535。
- 模板必须是 lite/standard/full。
- output_path 必须是绝对路径。
- cache_dir 必须是绝对路径。
- URL scheme 必须合法。
