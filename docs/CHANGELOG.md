# Changelog

## 2026-10-04

- 增加账号密码登录与前端注册，注册成功自动登录；普通用户的工作区、发布订阅与浏览器草稿按账号隔离。
- 增加原生安装器和 `scn` 中文菜单，菜单第 15 项可开启或关闭注册并显示开关状态。
- 修复退出登录请求失败时仍跳转登录页的问题；网络或接口错误会显示提示并允许重试。
- 管理登录已配置时，健康检查仅向已授权管理员提供版本与数据路径；本机监听经反代开放时也遵守该限制。

## 2026-04-26

- 将 AnyTLS 从 experimental 改为 V1 必选协议。
- 将 WireGuard 从 experimental 改为 V1 必选协议。
- 增加 `uri_anytls.go`、`uri_wireguard.go`、`wgconf.go` 任务要求。
- 增加 AnyTLS parser 字段映射、renderer 输出要求和 golden tests。
- 增加 WireGuard URI 与标准配置片段 parser 要求、renderer 输出要求和 golden tests。
- 更新验收标准：AnyTLS / WireGuard 不允许推迟到 V2，不允许只保留 Raw。
