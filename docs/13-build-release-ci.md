# 13. 构建、发布与 CI

## Makefile

仓库根目录创建：

```makefile
APP=subconv-next

.PHONY: test build clean run

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/$(APP) ./cmd/subconv-next

run:
	go run ./cmd/subconv-next serve --config ./testdata/config/basic.json

clean:
	rm -rf bin dist
```

## 本地构建

```sh
make test
make build
./bin/subconv-next version
```

## 多架构交叉编译

先提供普通 Linux 交叉编译产物：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/subconv-next_linux_amd64 ./cmd/subconv-next
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o dist/subconv-next_linux_arm64 ./cmd/subconv-next
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o dist/subconv-next_linux_armv7 ./cmd/subconv-next
CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -o dist/subconv-next_linux_mipsle_softfloat ./cmd/subconv-next
```

## OpenWrt SDK 构建策略

V1 允许两条路线：

### 路线 A：SDK 内构建 Go

如果 SDK 和 feeds 支持 Go helper，则在 OpenWrt Makefile 中直接编译。

### 路线 B：预构建二进制打包

如果 SDK 内 Go 构建不稳定，先用 GitHub Actions 交叉编译 binary，再让 OpenWrt package 安装对应架构 binary。

Codex 优先保证路线 B 可用。

## GitHub Actions

创建 `.github/workflows/ci.yml`：

- checkout
- setup-go
- go test
- build linux amd64/arm64/armv7/mipsle
- upload artifacts

### 发布版本

版本号统一保存在 `internal/buildinfo/VERSION`，当前为 `1.5.0`。普通 `go build`、原生源码安装、网页显示和 GitHub Release 构建都使用该版本；显式的 `-X main.version=...` 仍可用于指定构建版本。

`.github/workflows/auto-release.yml` 在 `main` 的版本文件发生变化时自动发布。也可在 [Actions 发布工作流](https://github.com/4444654/subconv-next/actions/workflows/auto-release.yml) 手动选择 **Run workflow**；版本输入可留空，填写时必须与版本文件一致，格式为 `主版本.次版本.修订号`。Fork 仓库若尚未开启 Actions，应先在 Actions 页面启用工作流，再手动运行首次发布。

发布流程先进行 Go、原生安装器和前端交互回归检查，再构建 Linux 多架构二进制、portable OpenWrt IPK 与 SHA-256 校验清单。先创建草稿并上传所有资源，再公开 Release，避免安装器获取未上传完成的发布。已有 Release 或 tag 会阻止覆盖，下一次发布应递增版本文件。

GHCR 镜像推送使用 `GITHUB_TOKEN`；镜像发布失败不会阻断原生二进制和 IPK 发布。可选配置 `GHCR_USERNAME` 与具有 `write:packages` 的 `GHCR_TOKEN`。已发布的不可变 tag 不能复用。

网页更新检查和原生安装器只使用 [本仓库 Releases](https://github.com/4444654/subconv-next/releases)。当前仓库没有兼容发布包时，原生安装器自动编译源码；不使用缺少账号管理功能的旧上游二进制。

## Release Artifacts

V1 release 包含：

```text
subconv-next-linux-amd64
subconv-next-linux-arm64
subconv-next-linux-armv7
subconv-next-linux-mips-softfloat
subconv-next-linux-mipsle-softfloat
subconv-next_<version>-1_aarch64_generic.ipk
checksums.txt
```

## 版本命名

Release 使用 `v1.5.0` 这样的 tag；CLI `subconv-next version` 输出 `1.5.0`。后续发布修改版本文件，例如 `1.5.1`，提交到 `main` 后触发工作流。
