# Magpie 本地共存版

目标是在保留上游结构的前提下，让 Magpie 与 Codex Router、CC Switch 等现有工具顺序切换使用。改动在 `codex/magpie-router-coexistence` 分支维护；当前不向远端推送。

## 配置切换

- Codex：接管前保存 endpoint、provider、catalog 和模型设置；退出时归还基础设施配置。新版上游为历史线程保留 `[model_providers.magpie]` 表，共存版也保留该表，避免旧线程无法恢复；恢复后它不再是当前 provider。新插入的根级 TOML 键放在托管块之外，避免 `model_provider` 随 Codex Router 的托管块重建而丢失。
- Claude Code：保存原有 endpoint、token、模型别名和 subagent 设置；退出时恢复。代理、主题等其他设置不变。
- `default` 清除选定的主模型；它不会在重复执行时删掉已经恢复的外部 endpoint/provider/catalog。如果其他工具已接管 endpoint，旧恢复记录不会覆盖新工具的配置。
- 多步配置改动先写入私有临时文件，全部成功后才发布；恢复记录在成功后清除。配置保存失败可以重试。

配置与恢复记录由新版适配器在私有临时目录中一起编辑；发布失败会回滚。配置事务会检查编辑期间的文件变化，但它不是其他工具共同遵守的进程锁。请顺序操作配置管理器，不要同时让两套工具接管同一个 agent。网关也不是自动串联的；同一时刻 agent 的入口由一个工具负责。

## Codex 登录由谁管理

默认是**外部管理**，不需要设置环境变量：

- Magpie 每次从当前登录读取凭据，有效时可用于请求。
- 凭据接近到期时，明确提示在 Codex 或现有账号管理器中刷新/重新登录，然后重试。Magpie 不自动启动原工具或替它刷新。
- 已保存的 Codex 账号也遵守此规则，后台保活跳过这些账号。
- 在 Magpie 中添加、切换 Codex 登录或删除当前 Codex 登录会返回外部管理提示，避免覆盖或移除原工具的账号。非当前的保存副本仍可移除。
- Claude 订阅登录的上游管理逻辑不在本次改动范围内；Claude 配置恢复不代表它的 OAuth 多持有者问题也已解决。

仅在**隔离、由 Magpie 独占管理的 Codex 登录环境**中，才可设置 `MAGPIE_CODEX_AUTH_OWNER=magpie`。这会启用添加、切换和刷新。它不是给 Codex Router/CC Switch 共用同一凭据时的开关：独占模式仍需要调用方保证单一所有者。

独占模式的写回使用 0600 权限，保存失败会返回错误，刷新途中发现登录已改变或被移除时停止该请求并提示重试。写回检查不构成跨工具的原子锁。

## 构建和使用

macOS 本地打包：

```sh
make local-app
```

生成 `dist/Magpie-Coexistence.app`。默认并发为 2、Go 构建并行为 1，复用 Go 缓存。应用使用独立 bundle ID `local.magpie.coexistence`，不注册官方 `magpie://` URL scheme；采用本地 ad-hoc 签名，不是 Apple 公证的发行版。

可用 `LOCAL_APP` 指定输出位置。程序版本为 `coexistence-dev-<commit>`，不是 release 版本，上游更新器不会覆盖它。不要把 `LOCAL_VERSION` 改成语义化 release 版本。

该包包含 GUI、CLI、TUI 和网关；可直接运行包内 `Contents/MacOS/magpie --version`。正式使用时沿用 Magpie 的配置目录和端口；独立应用名称并不隔离用户数据。

开发和测试使用仓库外的临时 HOME、XDG_CONFIG_HOME、XDG_CACHE_HOME，以及伪造登录。不要直接用真实账号做刷新竞态测试。构建产物不自动安装、启动，也不会自动接管现有 agent。

## 验证

运行前按 [provider-plugins.md](docs/subsystems/provider-plugins.md#verification) 隔离 HOME 和 agent 环境变量，并固定已有 Go 缓存；下面列出检查命令。插件测试使用仓库 CI 指定的 Bun 1.4.2，以及已安装 Node 的实际二进制。不要把依赖 HOME 的版本管理器 shim 当作隔离环境中的 Node，否则它会尝试重新安装运行时。

```sh
GOMAXPROCS=2 go test -p 1 -timeout 15m -tags nogui ./...
GOMAXPROCS=2 go vet -p 1 ./...
GOOS=linux CGO_ENABLED=0 GOMAXPROCS=2 go vet -p 1 -tags nogui ./...
GOOS=windows CGO_ENABLED=0 GOMAXPROCS=2 go vet -p 1 ./...
```

回归覆盖：配置往返和重复 default、外部工具接管、原有 Claude 别名、恢复失败后重试、恢复记录不可写、TOML 托管块位置、事务失败与冲突、有效/过期外部凭据、后台保活与账号操作边界、独占刷新写回及权限。

这些测试使用临时文件和本地假服务，不代替真实账号的线上验收。Gemini 及其他适配器保持上游行为。

## 后续维护

先保证工作区已保存，再在本地 main 拉取上游并将定制分支 rebase 到它；重新运行以上检查并用 `make local-app` 构建。密钥和机器配置不得提交；远端推送仍需遵守项目当前约定。

## 2026-10-09 上游更新

- 上游基线：`599d784f597342e6601bdc1d49b495fdf84e546f`（`v0.1.1143` 后 6 个提交）。本地 main 与此提交一致。
- 更新前来源：`87dfb7e3f5c4aefe8bff2f1077e6a6b62f27dea3`；保存在 `codex/magpie-router-coexistence-before-upstream-20261009`。常见应用安装目录及仓库中未找到已安装的 Magpie 包，因此这次记录的是仓库来源，不代表已部署版本。
- 共存逻辑迁移到新版 Codex/Claude 适配器，沿用上游历史线程、WSL、tier effort、账号路由等实现；`coexistence.go` 用这些适配器本身编辑暂存文件，`internal/edit/transaction.go` 同时保留配置、catalog 与恢复记录的失败重试能力。
- 原有回归测试继续保留。仅将“退出后删除 magpie provider 表”的旧断言改为检查当前路由恢复并保留历史线程表，匹配新版上游的线程恢复要求。
- 构建使用 Go 1.26.8，保持非 release 版本和独立 bundle ID。未操作真实账号、线上服务或安装目录。
- 插件验证使用 Bun 1.4.2 和 Node 24.19.0 的实际二进制。任务专用 Bun 来自官方发布包，下载包 SHA-256 为 `90987a3a16d7db556d886ac3d551e7b6d3edf0a1cf43acaed622e8676be1d12f`；没有修改系统运行时。
- 将 Codex/Claude 的事务包装临时停用后，`TestConfigFailedRestoreCanBeRetried` 的两个子测试均以“失败恢复改变配置或消耗恢复记录”报错；恢复包装后通过。
- 隔离 HOME 的全仓库测试：7,886 项通过、19 项按条件跳过、零失败。macOS/Linux/Windows vet 与 Linux/Windows 构建通过；改动的 Go 文件格式检查和 `git diff --check` 通过。两个网关账号切换测试显式启用伪造登录的独占管理模式，默认账号保护不变。
- 保留共享 Go 模块/构建缓存以供后续构建；任务运行时位于 `.git/magpie-validation-runtime`，只用于复现隔离插件测试。跨平台临时二进制已移除；macOS 本地产物与来源清单保存在 `dist/`。
- 真实账号、跨设备同步、Windows/Linux 实机和 GUI Playwright 未验证；这次没有修改上游 GUI 资源。
