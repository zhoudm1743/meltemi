# Stage 1C Multiline Text and Undo/Redo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 Input/Password 增加撤销/重做，并新增支持多行编辑、选择、滚动和历史的公开 TextArea 组件及真实演示。

**Architecture:** 在共享 rune textBuffer 上增加编辑快照和历史分组，单行与多行复用同一套撤销语义。multiTextBuffer 在共享选择上增加行列操作，TextAreaWidget 负责多行排版、滚动和事件，不引入新的后端。

**Tech Stack:** Go 1.26、既有 ui/textedit、InputWidget、driver/GLFW/OpenGL/text；不新增第三方依赖。

**Spec:** `docs/superpowers/specs/2026-11-04-text-input-css-ime-expanded-design.md` 的 1C 节，以及 `doc/总体设计.md` 的文本与输入约束。

## Global Constraints

- 不使用 Fyne、WebView、HTML 或 JavaScript，不新增依赖。
- 本阶段不实现 CSS、IME、自动软换行、富文本或行号。
- 所有编辑、历史和选择索引均为 rune；UI 状态只在 UI 线程。
- 连续普通输入合并为一个编辑单元；Backspace/Delete、选择替换、剪切、粘贴、SetValue 分别独立成单元。
- 新编辑清空 redo；历史最多 100 个编辑单元，Undo/Redo 同时恢复文本、cursor 和 anchor。
- Ctrl+Z 撤销，Ctrl+Y 和 Ctrl+Shift+Z 重做；无历史时安全 no-op。
- 当前非 Git 仓库，不自动初始化或提交；不使用 sudo。

## Review Focus

- 连续输入与选择替换：合并规则不得把多次 Backspace 或独立粘贴错误合并；由 Task 1 的 `TestHistoryGroupingAndSelectionRestore` 固定。
- 第 101 个编辑单元和 redo 分支：最旧历史被丢弃，新编辑后 redo 为空；由 Task 1 的 `TestHistoryLimitAndRedoInvalidation` 固定。
- 中文、emoji、尾随换行和空行：上下移动及行列计算不得错位；由 Task 2 的 `TestMultiBufferVerticalMovement` 固定。
- 长行水平滚动与多行垂直滚动：caret 和 selection 必须使用同一 OriginX/行偏移；由 Task 3 的 `TestTextAreaScrollingAndOrigin` 固定。
- Input 的单行规范化与历史：Undo 恢复的历史值仍不得含 CR/LF；由 Task 3 的 `TestInputUndoRedoAndSingleLineInvariant` 固定。

---

### Task 1: 共享撤销/重做历史与控制键

**Files:**
- Modify: `ui/textedit.go`
- Modify: `ui/textedit_test.go`
- Modify: `internal/driver/types.go`
- Modify: `internal/driver/glfw/events.go`
- Modify: `internal/driver/glfw/events_test.go`

**Interfaces:**
- Consumes: 现有 textBuffer 的 runes/cursor/anchor 和编辑方法。
- Produces: `textBuffer.Undo() bool`、`textBuffer.Redo() bool`、`KeyZ`、`KeyY`，供 Input/TextArea 使用。

- [ ] **Step 1: 写失败的历史测试**

覆盖：

```go
func TestHistoryUndoRedoRestoresValueCursorAnchor(t *testing.T)
func TestHistoryGroupingAndSelectionRestore(t *testing.T)
func TestHistoryLimitAndRedoInvalidation(t *testing.T)
func TestHistorySkipsNoopEdits(t *testing.T)
func TestHistoryPasteAndSetValueAreIndependent(t *testing.T)
func TestMapUndoRedoKeys(t *testing.T)
```

连续 Insert('a','b','c') 一次 Undo 回到初始值；Backspace 两次需两次 Undo。选择替换必须恢复替换前文本和 anchor。历史 cap=100 时只保留最新 100 个单元。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./ui -run 'TestHistory|TestMapUndoRedoKeys' -v`
Expected: FAIL，历史与按键尚未实现。

- [ ] **Step 3: 实现快照、历史分组和 Undo/Redo**

```go
type editSnapshot struct { value string; cursor, anchor int }
func (b *textBuffer) Undo() bool
func (b *textBuffer) Redo() bool
```

在所有真实修改入口记录 before 快照，保存 after 用于 redo 分支。无修改不记录，选择/光标移动不记录。普通 Insert 合并，其他编辑按设计独立成单元；新编辑清 redo，超过 100 丢弃最旧。Undo/Redo 恢复 runes、cursor 和 anchor，并同步失效历史分组状态。

- [ ] **Step 4: 实现 KeyZ/KeyY 并运行测试**

按键追加到现有枚举末尾，mapKey 使用相同映射方式，不改变既有键值和 Rune 事件。

Run: `go test ./ui -run 'TestHistory|TestMapUndoRedoKeys' -v`
Expected: PASS。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add ui/textedit.go ui/textedit_test.go internal/driver
git commit -m "feat: add text edit history"
```

### Task 2: 多行文本行列模型

**Files:**
- Create: `ui/multiedit.go`
- Test: `ui/multiedit_test.go`

**Interfaces:**
- Consumes: Task 1 的 textBuffer、编辑与历史语义。
- Produces: `multiTextBuffer`，供 TextAreaWidget 使用。

- [ ] **Step 1: 写失败的多行模型测试**

覆盖：

```go
func TestMultiBufferLinesAndTrailingEmptyLine(t *testing.T)
func TestMultiBufferVerticalMovement(t *testing.T)
func TestMultiBufferLineHomeEndAndShift(t *testing.T)
func TestMultiBufferNewlineAndUnicode(t *testing.T)
func TestMultiBufferSelectionAcrossLines(t *testing.T)
```

示例 `"a中\n🙂\n"` 为三行，第三行为空。上下移动尽量保持当前列并钳制到目标行尾；Home/End 作用于当前行而非整个文本。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./ui -run TestMultiBuffer -v`
Expected: FAIL，multiTextBuffer 尚未实现。

- [ ] **Step 3: 实现 multiTextBuffer**

```go
type multiTextBuffer struct { /* shared textBuffer */ }
func newMultiTextBuffer(value string) *multiTextBuffer
func (m *multiTextBuffer) Lines() []string
func (m *multiTextBuffer) LineStart(index int) int
func (m *multiTextBuffer) LineEnd(index int) int
func (m *multiTextBuffer) LineIndex(index int) int
func (m *multiTextBuffer) Column(index int) int
func (m *multiTextBuffer) MoveVertical(delta int, extend bool)
func (m *multiTextBuffer) InsertNewline()
```

复用共享 Insert/Backspace/Delete/Move/Home/End/Selection/Undo/Redo。Lines 保留尾随空行；换行形成独立编辑单元，不进入普通字符输入合并组。

- [ ] **Step 4: 运行多行模型测试**

Run: `go test ./ui -run TestMultiBuffer -v`
Expected: PASS。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add ui/multiedit.go ui/multiedit_test.go
git commit -m "feat: add multiline text model"
```

### Task 3: TextArea 组件与 Input 历史快捷键

**Files:**
- Create: `ui/textarea.go`
- Create: `ui/textarea_draw.go`
- Modify: `ui/input.go`
- Modify: `ui/input_test.go`
- Test: `ui/textarea_test.go`
- Test: `ui/textarea_draw_test.go`

**Interfaces:**
- Consumes: Task 1 的历史、Task 2 的 multiTextBuffer、既有 painter/eventRouter/Clipboard/CaretOffsets/OriginX。
- Produces: `TextAreaWidget`、`TextArea` 和 InputWidget 的 Undo/Redo。

- [ ] **Step 1: 写失败的 TextArea 和 Input 历史测试**

覆盖：

```go
func TestTextAreaEditingBindingAndHistory(t *testing.T)
func TestTextAreaVerticalNavigationAndSelection(t *testing.T)
func TestTextAreaScrollingAndOrigin(t *testing.T)
func TestTextAreaClipboardAndDisabled(t *testing.T)
func TestTextAreaPlaceholderAndFocus(t *testing.T)
func TestInputUndoRedoAndSingleLineInvariant(t *testing.T)
```

`TextArea(&value).Placeholder("说明")` 自然默认 320x160，支持 Column 拉伸；Input/Password 也提供 Undo/Redo 方法及 Ctrl+Z/Y/Ctrl+Shift+Z。

- [ ] **Step 2: 运行测试并确认失败**

Run: `go test ./ui -run 'TestTextArea|TestInputUndoRedo' -v`
Expected: FAIL，TextArea 与历史接线尚未实现。

- [ ] **Step 3: 实现公开 TextAreaWidget**

```go
func TextArea(value *string) *TextAreaWidget
func (t *TextAreaWidget) Placeholder(text string) *TextAreaWidget
func (t *TextAreaWidget) Value() string
func (t *TextAreaWidget) SetValue(value string)
func (t *TextAreaWidget) Enabled() bool
func (t *TextAreaWidget) SetEnabled(enabled bool)
func (t *TextAreaWidget) Undo() bool
func (t *TextAreaWidget) Redo() bool
func (i *InputWidget) Undo() bool
func (i *InputWidget) Redo() bool
```

实现 component、focusableComponent、pointerTarget、faceReceiver。Enter 插入换行，Ctrl+A/C/V/X 沿用共享行为，SetValue 同步绑定和显示，禁用清焦点与手势。

- [ ] **Step 4: 实现多行绘制、滚动和 Input 历史快捷键**

逐行绘制文本、placeholder、选择背景和 1px caret，使用统一 CaretOffsets/OriginX；scrollY 以行为单位保证 cursor 行可见，scrollX 处理当前行长文本，所有内容 SetClip/ClearClip。鼠标点击定位 rune，拖动跨行选择，释放不丢失焦点。

Input/Password 的 Undo/Redo 调用共享历史，Ctrl+Z/Y/Ctrl+Shift+Z 接入事件路由，单行 CR/LF 不变量保持。

- [ ] **Step 5: 运行组件与全量测试**

Run:

```bash
go test ./ui -v
go test -race ./ui ./internal/app
go test ./...
```

Expected: PASS，覆盖多行、历史、选择、滚动和禁用。

- [ ] **Step 6: 提交（仅已明确授权时）**

```bash
git add ui internal/driver
git commit -m "feat: add multiline textarea and undo shortcuts"
```

### Task 4: 多行编辑真实演示

**Files:**
- Create: `cmd/wind-notes/main.go`

**Interfaces:**
- Consumes: 前三个任务的公开 API。
- Produces: 可运行的 `wind-notes` 和 `--smoke` 验证。

- [ ] **Step 1: 先运行缺失入口确认 RED**

Run: `go run ./cmd/wind-notes --smoke`
Expected: FAIL，入口尚不存在。

- [ ] **Step 2: 实现多行笔记窗口**

用 `TextArea(&notes).Placeholder("输入笔记")`、Undo/Redo Button 和状态 Label 展示链式 API。演示注册中文字体（可选 `--font`），不显示内部类型，不直接操作 renderer。真实用户可编辑多行、选择、撤销和重做。

- [ ] **Step 3: 实现 smoke 并运行完整验证**

`--smoke` 通过 Post 设置多行文本、执行普通编辑、Undo/Redo，断言绑定值、选择和状态，再关闭窗口。失败记录首错并非零退出且 Quit 防挂死。

Run:

```bash
go test ./...
go test -race ./ui ./internal/app
go vet ./...
go build ./cmd/wind-notes
go run ./cmd/wind-notes --smoke
```

Expected: 全部退出码 0，真实多行窗口创建并释放，无残留进程。删除构建产物。

- [ ] **Step 4: 验证真实桌面交互**

Run: `go run ./cmd/wind-notes`

Expected: 可输入中文和换行、上下导航、跨行选择、复制剪切粘贴、Undo/Redo、垂直滚动正常；无法真人或高 DPI 验证必须披露。

- [ ] **Step 5: 提交（仅已明确授权时）**

```bash
git add cmd/wind-notes
git commit -m "feat: add multiline notes demo"
```
