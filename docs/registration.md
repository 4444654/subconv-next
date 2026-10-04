# 前端注册与用户账号

此仓库的注册入口位于 `/login` 登录页，点击 **注册账号** 后填写账号、密码、确认密码。也可通过 `/login?mode=register` 直接打开注册表单。注册成功自动登录，并进入自己的订阅转换工作区；会话有效期为 12 小时。

账号要求为 3–64 位英文字母、数字或 `_ . @ -`，注册账号不区分大小写。密码为 8–72 字节，支持空格与特殊字符，确认密码必须完全一致。`admin` 和当前配置的管理员账号名保留给管理员。

## 升级与开关

已有原生安装需要同时更新程序和管理脚本：

```bash
curl -fL --retry 2 https://raw.githubusercontent.com/4444654/subconv-next/main/subconv-next-onekey.sh -o /tmp/subconv-next-onekey.sh && bash /tmp/subconv-next-onekey.sh update
```

升级完成后刷新浏览器页面。默认开放注册；服务器管理命令：

```bash
scn registration off
scn registration on
```

其它部署方式使用 `SUBCONV_REGISTRATION_ENABLED=false` / `true`，对应 JSON/UCI 配置项为 `service.registration_enabled`。修改需重启进程。关闭注册同时隐藏前端入口并拒绝新账号注册，已注册用户继续登录。管理登录尚未配置、匿名公开转换模式和无鉴权预览模式均不开放注册。

通过 HTTPS 反代访问时，先执行 `scn url https://实际访问域名`，保持协议、域名和端口与浏览器一致。

## 用户隔离

- 新注册账号为普通用户，原配置中的管理员账号保留管理员权限。
- 用户只能通过编辑 API 操作属于自己的工作区和发布订阅；持有另一个用户的工作区 ID 或发布 ID也不能修改、删除或读取其配置。
- 发布的订阅下载链接仍是供订阅客户端使用的独立链接，持有链接可下载 YAML。
- 每个用户在同一浏览器中的本机草稿使用独立存储键，管理员现有草稿继续使用原来的存储键。
- 工作区继续采用隐私会话模式；刷新页面会创建新工作区。需要下次恢复的配置请保存本机草稿或导出。
- 普通用户受公开转换的来源数量、内容大小、私有网络访问、TLS 验证和抓取并发限制，不提供服务器管理权限。

## 存储与接口

账号持久化于数据目录下的 `accounts.json`（原生安装为 `/var/lib/subconv-next/accounts.json`）。文件权限为 `0600`，保存 bcrypt 哈希，不保存明文密码；重启和安装更新保留账号。账号上限为 256，注册限制为每个来源每分钟 6 次、全局每分钟 20 次，密码哈希处理最多同时 4 个。

`POST /api/auth/register` 接收 JSON：

```json
{
  "username": "your-account",
  "password": "your-new-password",
  "confirm_password": "your-new-password"
}
```

成功返回 `201`、登录 Cookie、`role: "user"`、`user_id` 和 `csrf_token`。重复账号返回 `409`；无效输入返回 `400`；关闭注册返回 `403`；限流或账号已满返回 `429`。`GET /api/auth/session` 同时返回 `registration_enabled` 和已登录账号的身份；`POST /api/auth/login` 继续使用 `username`、`password`。用户通过 Cookie 登录后的写操作必须带会话对应的 `X-SubConv-CSRF` 请求头。

## 验证

```bash
go test ./...
go test -race ./internal/api -run 'TestRegistration|TestRegistered|TestConcurrentRegistration'
python3 scripts/test-native-install.py
node scripts/test-auth-ui.js
```
