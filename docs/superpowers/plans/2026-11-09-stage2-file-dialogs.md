# Stage 2 File Dialogs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Linux 上通过 XDG Desktop Portal 提供可取消的异步打开/保存文件对话框，回调统一进入 UI 线程，不支持时返回明确错误。

**Architecture:** `internal/desktop` 复用已有有界 D-Bus 建连，封装 `org.freedesktop.portal.FileChooser` 和 Request.Response/Close。公共 `ui.App` 只传递不可变配置和路径结果，portal 服务不读取 Window 或组件。先订阅按发送者名预测的 Request 路径再调用方法，避免快速响应丢失。

**Tech Stack:** Go 1.26、既有 godbus、XDG Desktop Portal；不新增依赖。

**Spec:** `doc/总体设计.md` 第 3.2、5.1、5.5、7.4、10、12、13 阶段2、15节；协议 https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.FileChooser.html

## Global Constraints

- 不使用 GTK、WebView、Fyne 或新依赖；系统对话框不接受 Wind CSS。
- D-Bus 调用/建连有界，portal 线程不得直接访问 UI；回调只经 `App.Post`。
- 所有文件 URI 必须是 `file://`，转换为本地路径并正确反转义；取消不是成功空结果。
- App.Quit/Destroy/主动 Close 必须结束请求并退订；晚到响应不得重复回调。
- 非 Git 原地工作区，不初始化/提交；用快照 diff 审查。

## Review Focus

- 用户取消、portal 消失和响应早于调用返回：不得丢失或重复回调；Task 1 的 `TestPortalResponseRaceAndCancellation` 固定。
- 非 file URI、非法转义、多选与目录选择：结果验证和 options 类型必须准确；Task 1 的 `TestFileChooserOptionsAndURIResults` 固定。
- 在回调中关闭窗口/App 或再次打开对话框：不得死锁；Task 2 的 `TestDialogCallbackReentrancyAndQuit` 固定。

---

### Task 1: FileChooser portal 服务

**Files:**
- Create: `internal/desktop/dialog.go`, `internal/desktop/dialog_linux.go`, `internal/desktop/dialog_unsupported.go`
- Test: `internal/desktop/dialog_linux_test.go`

**Interfaces:**
- Produces: 内部 `DialogService` 的 `Capabilities`, `Open`, `Save`, `Close`；不可变 `DialogOptions`, `FileFilter`, `DialogResult`；稳定错误 `ErrDialogUnavailable/ErrDialogCanceled`。
- 支持 OpenFile（multiple/directory）和 SaveFile；title、accept label、modal、filters、current folder/name/file；Request.Close 主动取消。

- [ ] **Step 1: 写失败测试** `TestFileChooserOptionsAndURIResults`、`TestPortalResponseRaceAndCancellation`、`TestDialogUnavailableAndTimeout`，断言实际 D-Bus destination/path/method/signature、filter `(sa(us))`、NUL 结尾和 response codes 0/1/2。
- [ ] **Step 2: 运行 `go test ./internal/desktop -run 'Test(FileChooser|Portal|Dialog)' -v`，确认因服务缺失失败。**
- [ ] **Step 3: 实现预测 Request 路径的提前订阅、OpenFile/SaveFile、响应验证、URI 转换、取消和 Close。** 使用唯一 token；每个请求有独立状态，Close 幂等，外部调用不持有响应状态锁。
- [ ] **Step 4: 运行 focused tests、focused race 和 gofmt。**

### Task 2: 公共 App API、能力查询与演示

**Files:**
- Create: `ui/dialog.go`, `ui/dialog_test.go`, `cmd/wind-dialog/main.go`, `cmd/wind-dialog/main_test.go`
- Modify: `ui/app.go`, `ui/capability.go`, `internal/desktop/tray.go`

**Interfaces:**
- Produces: `OpenFileOptions`, `SaveFileOptions`, `FileFilter`、`App.OpenFile(options, func(paths []string, err error)) (*FileDialog,error)`、`App.SaveFile(options, func(path string, err error)) (*FileDialog,error)`、`FileDialog.Close() error`。
- `Capabilities` 增加 `FileDialog bool`；`ui.ErrDialogUnavailable/ErrDialogCanceled` 支持 errors.Is。
- Options 提供 Parent *Window 但只转换为 portal 标识，不让 desktop 读取 Window；当前 X11 parent 为空字符串并记录该限制。

- [ ] **Step 1: 写失败测试** `TestPublicOpenSaveDialogLifecycle`、`TestDialogCallbackReentrancyAndQuit`、`TestDialogCapabilityAndValidation`，覆盖回调 UI 线程、nil callback、关闭后重复响应、Quit 清理。
- [ ] **Step 2: 运行 `go test ./ui ./internal/desktop -run 'Test(Dialog|PublicOpen|FileDialog)' -v`，确认失败。**
- [ ] **Step 3: 实现公共 DTO/校验/App 跟踪和 callback Post。** callback 执行前验证 dialog generation/alive；Close 后结果无效，普通取消只回调一次。
- [ ] **Step 4: 添加真实 `cmd/wind-dialog`。** 提供打开、多选、保存按钮及 `--smoke`；bounded smoke 至少完成真实 portal 创建和主动 Close，若被取消/不支持须如实输出。
- [ ] **Step 5: 运行 `go test ./...`、race、vet、gofmt 和真实 DDE/私有 portal smoke；记录视觉验证边界。**
