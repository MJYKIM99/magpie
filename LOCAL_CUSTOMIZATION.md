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

## 2026-10-13 OpenCode Go DeepSeek 后端

Codex 的入口和模型目录继续由 Codex Router 管理。新增路线为 `Codex → Router → Magpie → OpenCode Go`，模型名为 **DeepSeek V4.1 Flash (via Magpie)**，路由标识 `magpie-go/opencode-go/deepseek-v4.1-flash`，默认推理强度为 `max`。原有 Web GPT 和直接 OpenCode Go 路线保留。当前接入只服务 Codex；Claude 的配置和订阅登录未纳入接入。

Router 使用本机 `http://127.0.0.1:3425/v1` 的 Responses 接口，以及 Magpie 为它创建的调用密钥。该密钥仅允许 `opencode-go/deepseek-v4.1-flash`；实际 Go Key 在 Magpie 本机表单添加，文档和代码都不保存密钥。Magpie 的该供应商没有配置跨供应商 fallback，Router 的全局跨模型 failover 当前也是关闭状态。模型不可用时应返回错误，不切到直接 DeepSeek API 或订阅账号。

供应商和模型存入 Router 的 `generic-providers.json`、`user-models.json` 和 `model-picker.json` 本机覆盖层，复用其配置发布及服务重启事务；Router 仓库源代码没有为此接入而修改。请保持 Magpie 运行，并在 Codex 模型选择器选择上面的模型。不要同时在 Magpie 的 Codex 页面执行连接或模型切换，以免两个配置管理器轮流改写 Codex 的入口。

初次真实验证发现当前共存版会把没有推理记录的工具历史原样传给 OpenCode Go，得到 400 `reasoning_text ... must be passed back`。共存分支回移上游 `f4cc7459` 的 Responses 修复：补齐空缺，并把每个调用工具或接续工具结果的助手步骤的推理放在最前；修复同时覆盖透传和协议转换。既有推理、加密字段和非 DeepSeek 的协议行为由回归验证保留。Chat 和 Messages 的同类上游修复未回移，不能据此宣称 Claude 已接入。

本次是必要兼容修复和本机接入，**没有完成全量上游升级**。基线仍为 `5e611c5257b7928df075f0bebd297266cfe6f2f1`，候选版本为 `coexistence-dev-5e611c52-deepseek-ca57ef0040`；精确源文件哈希、签名和二进制 SHA-256 记录在 `.git/magpie-router-20261013-evidence/artifact-manifest.json`，安装结果记录在同目录的 `installation-manifest.json`。

验证使用隔离 HOME，只复制用户明确选择的 OpenCode Go 供应商；Codex、Claude 和其他订阅凭据没有进入测试环境。真实完整链路验证了文本、工具调用、无推理记录的工具结果和压缩；一次流式检查未通过，聚焦复查收到了正确文本和 `response.completed`。实际命名空间工具调用及带真实推理的第二轮工具往返通过。另有假服务验证调用密钥范围及调用方 ChatGPT 凭据不进入供应商请求。图片、多代理协作、Claude 接入以及其他操作系统实机均未验证；GUI 资源没有修改。

配置及旧安装包备份位于 `~/.codex/codex-router/magpie-integration-backups/20261013-1791914604409`。回滚配置时必须保留接入后用户的其他改动，按路由标识移除本次覆盖层记录并通过 Router 重新发布；不要直接用旧文件覆盖正在使用的配置。旧包用于回滚本次二进制兼容补丁。验证日志、发布结果和临时进程清理记录保存在 `.git/magpie-router-20261013-evidence/`。

安装和发布后检查：全仓库隔离回归 7,891 项通过、19 项条件跳过、零失败；三平台 vet 和构建通过，格式及 diff 检查通过。Router 的真实模型列表接口返回 200 并列出新增模型，调用凭据就绪。原目录的 13 个模型全部保留；新目录共 67 项，包含此前配置的外部路线及本次路线，没有把原有可见模型隐藏。

发布时发现原 Codex 的 `custom` provider 表指向旧式 Router 地址，导致目录只生成原生模型。首次发布校验失败后事务回滚。随后通过 Router 自带 `signed-enable` 管理器迁移到带调用方权限的地址，再成功发布；根级 `model` 和 `model_provider` 保持原值，登录模式仍需 Codex 自己的登录。迁移后的对比还发现原有 `service_tier = "priority"` 丢失，已恢复到托管块之外。最终 TOML 对比只剩 `custom` 和 Router 相关 provider 表的预期变化，其余配置保留；Codex 登录及 Claude 配置/登录文件的大小、mtime、inode 均未变化。完整敏感地址不得粘贴到聊天或提交。

构建目录保留约 43 MB 的候选应用，备份目录保留同等大小的旧应用；共享 Go 缓存供后续构建复用。临时跨平台二进制和测试服务已清理，正式 Magpie 与 Router 后台服务继续运行。Codex 应用未重启；用户重新打开后可选择新增模型。

## 2026-10-20 上游更新候选

上游基线为 `0fe039f14deb0279fa052303ecd242980fc913a9`（`v0.1.1299` 后 9 个提交），本次获取时比原共存版多 1,640 个上游提交。候选在独立工作区 `/Users/yi/.codex/worktrees/magpie-upstream/magpie`、分支 `codex/magpie-upstream-20261020` 中准备。更新前完整源码（包括 10 月 13 日已安装的 DeepSeek 修复）保存在 `codex/magpie-router-coexistence-before-upstream-20261020`，提交 `e9d8f735`；原安装二进制 SHA-256 仍为 `7abf572b6944a4fa4dc82fab30e23fcb93a33a36bb2953d6533cbe7b66709998`，已与安装清单及当时的脏文件哈希核对。没有把旧分支名当作安装来源证明。

迁移保留配置恢复事务、重复 default、外部工具接管和 Codex Router 托管块保护。新版适配器的 `CODEX_HOME`、Claude 配置目录、模型目录中用户字段和通知逻辑沿用上游实现。上次回移的 DeepSeek 修复现在已在上游，使用上游 Responses、Chat、Messages 的完整实现；原有 26 个本地新增回归测试全部保留，没有因重复回移而退回旧网关代码。

Codex 新增的文件/钥匙串凭据存储仍默认由外部管理。新的 `StartSignInWith` 和账号导入入口也返回所有权提示；独占模式刷新后核对原存储及刷新凭据，再通过上游存储接口保存。新增假钥匙串回归覆盖外部账号不刷新、不写回，以及刷新过程中另一工具换号时不覆盖。上游的伪造 Codex 登录/刷新测试显式启用独占模式，生产默认不变。

上游自 10 月 17 日起移除了 Grok、Kiro 等已退役订阅的内置请求实现，改由社区插件服务。更新包含这项上游迁移；这不表示允许 Codex、Claude 或其他应用共用同一账号的刷新权。当前任务没有运行真实账号迁移、读取凭据内容或修改 Router/Web GPT/Claude 的本机配置。

用户已明确批准本次低磁盘空间下的有界构建、测试和本机安装：最多 2 个 CPU，构建并行为 1，复用缓存，不创建模拟器。原安装应用尚未替换，候选也未接触真实账号。证据保存在主仓库 `.git/magpie-upstream-20261020-evidence/`。

扩大回归发现并修正了三项上游兼容差异：后台压缩的恢复记录纳入同一暂存事务；损坏模型目录的副本保存在实际目录，避免随临时目录清理；共存提示接入上游通知模板并补齐简体中文、繁体中文、日文和德文。伪造登录的 Agent 账号顺序测试显式启用独占模式。凭据写入失败测试保持本地规则：旧文件不变，且当前请求报告未保存的轮换，避免静默丢失刷新状态。两个钥匙串保护测试分别在反转外部所有权判断、反转并发换号检查时以真实行为错误失败，恢复代码后通过。

最新 5 个上游提交在验证期间发布，已纳入 `0fe039f1` 基线。此前完整 Provider 包通过 1,706 项、跳过 3 项；旧基线的 Agent 全量运行暴露上述兼容问题，修复后的针对性检查已通过。新的全仓库、三平台 vet/build、双引擎多语言通知和隔离调用链检查继续进行，未完成的检查不记为通过。
