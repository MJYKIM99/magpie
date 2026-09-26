# Magpie 本地共存版

目标是在保留上游结构的前提下，让 Magpie 与 Codex Router、CC Switch 等现有工具顺序切换使用。改动在 `codex/magpie-router-coexistence` 分支维护；当前不向远端推送。

## 配置切换

- Codex：接管前保存 endpoint、provider、catalog 和模型设置；退出时归还基础设施配置。新插入的根级 TOML 键放在托管块之外，避免 `model_provider` 随 Codex Router 的托管块重建而丢失。
- Claude Code：保存原有 endpoint、token、模型别名和 subagent 设置；退出时恢复。代理、主题等其他设置不变。
- `default` 清除选定的主模型；它不会在重复执行时删掉已经恢复的外部 endpoint/provider/catalog。如果其他工具已接管 endpoint，旧恢复记录不会覆盖新工具的配置。
- 多步配置改动先写入私有临时文件，全部成功后才发布；恢复记录在成功后清除。配置保存失败可以重试。

配置事务会检查编辑期间的文件变化，但它不是其他工具共同遵守的进程锁。请顺序操作配置管理器，不要同时让两套工具接管同一个 agent。网关也不是自动串联的；同一时刻 agent 的入口由一个工具负责。

## Codex 登录由谁管理

默认是**外部管理**，不需要设置环境变量：

- Magpie 每次从当前登录读取凭据，有效时可用于请求。
- 凭据接近到期时，明确提示在 Codex 或现有账号管理器中刷新/重新登录，然后重试。Magpie 不自动启动原工具或替它刷新。
- 已保存的 Codex 账号也遵守此规则，后台保活跳过这些账号。
- 在 Magpie 中添加、切换 Codex 登录会返回外部管理提示，避免覆盖原工具的账号。
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

```sh
GOMAXPROCS=2 go test -p 1 ./...
GOMAXPROCS=2 go vet -p 1 ./...
```

回归覆盖：配置往返和重复 default、外部工具接管、原有 Claude 别名、恢复失败后重试、恢复记录不可写、TOML 托管块位置、事务失败与冲突、有效/过期外部凭据、后台保活与账号操作边界、独占刷新写回及权限。

这些测试使用临时文件和本地假服务，不代替真实账号的线上验收。Gemini 及其他适配器保持上游行为。

## 后续维护

先保证工作区已保存，再在本地 main 拉取上游并将定制分支 rebase 到它；重新运行以上检查并用 `make local-app` 构建。密钥和机器配置不得提交；远端推送仍需遵守项目当前约定。
